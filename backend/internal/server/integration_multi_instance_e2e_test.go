//go:build integration

package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func testConcurrentIntegrationProjectionVersions(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	suffix := NewID("integration_e2e")
	tenantID := "tenant_" + suffix
	t.Cleanup(func() {
		_ = storeA.db.Where("tenant_id = ?", tenantID).Delete(&IntegrationInbox{}).Error
		_ = storeA.db.Where("external_tenant_id = ?", tenantID).Delete(&GatewayTenant{}).Error
	})
	baseTime := time.Now().UTC()
	event := func(eventID string, version int64, name string) GatewayIntegrationEvent {
		return GatewayIntegrationEvent{
			SchemaVersion: 1, EventID: eventID, EventType: "tenant.updated", AggregateType: "tenant",
			AggregateID: tenantID, TenantID: tenantID, Version: version, OccurredAt: baseTime.Add(time.Duration(version) * time.Second),
			TraceID: "trace_" + eventID, SourceService: "external-orchestrator",
			Payload: map[string]interface{}{"externalId": tenantID, "name": name, "status": StatusActive},
		}
	}
	created := event("evt_"+suffix+"_1", 1, "version one")
	created.EventType = "tenant.created"
	if _, err := storeA.ApplyGatewayIntegrationEvent(created); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, apply := range []struct {
		store *GormStore
		event GatewayIntegrationEvent
	}{{storeA, event("evt_"+suffix+"_2", 2, "version two")}, {storeB, event("evt_"+suffix+"_3", 3, "version three")}} {
		wg.Add(1)
		go func(store *GormStore, integrationEvent GatewayIntegrationEvent) {
			defer wg.Done()
			<-start
			_, err := store.ApplyGatewayIntegrationEvent(integrationEvent)
			errs <- err
		}(apply.store, apply.event)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var tenant GatewayTenant
	if err := storeA.db.First(&tenant, "external_tenant_id = ?", tenantID).Error; err != nil {
		t.Fatal(err)
	}
	if tenant.Version != 3 || tenant.Name != "version three" {
		t.Fatalf("expected latest integration projection, got %+v", tenant)
	}
}

func testConcurrentGatewayManagedUserProfiles(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	suffix := NewID("managed_user_e2e")
	tenantA, tenantB := "tenant_a_"+suffix, "tenant_b_"+suffix
	principalID := "principal_" + suffix
	baseTime := time.Now().UTC().Add(-time.Minute)
	event := func(eventID, eventType, aggregateType, aggregateID, tenantID string, version int64, occurredAt time.Time, payload map[string]interface{}) GatewayIntegrationEvent {
		return GatewayIntegrationEvent{
			SchemaVersion: 1, EventID: eventID, EventType: eventType, AggregateType: aggregateType,
			AggregateID: aggregateID, TenantID: tenantID, Version: version, OccurredAt: occurredAt,
			TraceID: "trace_" + eventID, SourceService: "external-orchestrator", Payload: payload,
		}
	}
	for _, seed := range []struct {
		store *GormStore
		event GatewayIntegrationEvent
	}{
		{storeA, event("evt_"+suffix+"_tenant_a", "tenant.created", "tenant", tenantA, tenantA, 1, baseTime, map[string]interface{}{"externalId": tenantA, "name": "Tenant A"})},
		{storeB, event("evt_"+suffix+"_tenant_b", "tenant.created", "tenant", tenantB, tenantB, 1, baseTime, map[string]interface{}{"externalId": tenantB, "name": "Tenant B"})},
		{storeA, event("evt_"+suffix+"_member_a", "tenant_member.added", "tenant_member", "member_a_"+suffix, tenantA, 1, baseTime.Add(time.Second), map[string]interface{}{
			"externalId": "member_a_" + suffix, "principalExternalId": principalID, "name": "Baseline user", "email": suffix + "@example.test",
		})},
		{storeB, event("evt_"+suffix+"_member_b", "tenant_member.added", "tenant_member", "member_b_"+suffix, tenantB, 1, baseTime.Add(time.Second), map[string]interface{}{
			"externalId": "member_b_" + suffix, "principalExternalId": principalID, "name": "Baseline user", "email": suffix + "@example.test",
		})},
	} {
		if _, err := seed.store.ApplyGatewayIntegrationEvent(seed.event); err != nil {
			t.Fatal(err)
		}
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce sync.Once
	callbackName := "test:managed-user-profile-lock:" + suffix
	if err := storeA.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "admin_users" {
			return
		}
		pauseOnce.Do(func() {
			close(started)
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				_ = tx.AddError(context.DeadlineExceeded)
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storeA.db.Callback().Update().Remove(callbackName); err != nil {
			t.Errorf("remove managed user callback: %v", err)
		}
	})

	older := event("evt_"+suffix+"_older", "tenant_member.updated", "tenant_member", "member_a_"+suffix, tenantA, 2, baseTime.Add(2*time.Second), map[string]interface{}{
		"externalId": "member_a_" + suffix, "principalExternalId": principalID, "name": "Older user", "email": "older-" + suffix + "@example.test", "status": StatusActive,
	})
	newer := event("evt_"+suffix+"_newer", "tenant_member.updated", "tenant_member", "member_b_"+suffix, tenantB, 2, baseTime.Add(3*time.Second), map[string]interface{}{
		"externalId": "member_b_" + suffix, "principalExternalId": principalID, "name": "Current user", "email": "current-" + suffix + "@example.test", "status": StatusActive,
	})
	olderDone := make(chan error, 1)
	go func() {
		_, err := storeA.ApplyGatewayIntegrationEvent(older)
		olderDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("older profile update did not reach the commit barrier")
	}
	newerDone := make(chan error, 1)
	go func() {
		_, err := storeB.ApplyGatewayIntegrationEvent(newer)
		newerDone <- err
	}()
	var newerErr error
	newerFinishedEarly := false
	select {
	case newerErr = <-newerDone:
		newerFinishedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-olderDone; err != nil {
		t.Fatal(err)
	}
	if !newerFinishedEarly {
		newerErr = <-newerDone
	}
	if newerErr != nil {
		t.Fatal(newerErr)
	}
	if newerFinishedEarly {
		t.Fatal("cross-tenant profile update bypassed the managed user lock")
	}

	var managed GatewayManagedUser
	if err := storeA.db.First(&managed, "external_principal_id = ?", principalID).Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := storeA.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Name != "Current user" || user.Email != "current-"+suffix+"@example.test" {
		t.Fatalf("newer cross-tenant profile did not win: %+v", user)
	}
}

func testConcurrentGatewayManagedOIDCBinding(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	suffix := NewID("managed_oidc_e2e")
	now := time.Now().UTC()
	issuer := "https://issuer.example.test/" + suffix
	subject := "subject_" + suffix

	type identityFixture struct {
		tenant    GatewayTenant
		principal GatewayPrincipal
		managed   GatewayManagedUser
		user      AdminUser
	}
	fixtures := make([]identityFixture, 0, 2)
	t.Cleanup(func() {
		for _, fixture := range fixtures {
			_ = storeA.db.Delete(&fixture.managed).Error
			_ = storeA.db.Delete(&fixture.principal).Error
			_ = storeA.db.Delete(&fixture.user).Error
			_ = storeA.db.Delete(&fixture.tenant).Error
		}
	})
	for _, label := range []string{"a", "b"} {
		tenant := GatewayTenant{
			ID: "tenant_" + label + "_" + suffix, ExternalTenantID: "external_tenant_" + label + "_" + suffix,
			Name: "OIDC tenant " + label, Status: StatusActive, Version: 1, SyncedAt: now,
		}
		if err := storeA.db.Create(&tenant).Error; err != nil {
			t.Fatal(err)
		}
		principal := GatewayPrincipal{
			ID: "principal_" + label + "_" + suffix, TenantID: tenant.ID, ExternalPrincipalID: "external_principal_" + label + "_" + suffix,
			ExternalMembershipID: "membership_" + label + "_" + suffix, DisplayName: "OIDC user " + label,
			Email: label + "-" + suffix + "@example.test", Status: StatusActive, Version: 1, SourceOccurredAt: now, SyncedAt: now,
		}
		if err := storeA.db.Create(&principal).Error; err != nil {
			t.Fatal(err)
		}
		user, err := createAdminUser(storeA.db, AdminUser{
			Username: "oidc_" + label + "_" + suffix, Name: principal.DisplayName, Email: principal.Email, Role: "user", Status: StatusActive,
		}, GenerateAdminSessionToken())
		if err != nil {
			t.Fatal(err)
		}
		managed := GatewayManagedUser{ExternalPrincipalID: principal.ExternalPrincipalID, AdminUserID: user.ID}
		if err := storeA.db.Create(&managed).Error; err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, identityFixture{tenant: tenant, principal: principal, managed: managed, user: user})
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce sync.Once
	var releaseOnce sync.Once
	releaseBinding := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBinding()
	callbackName := "test:managed-oidc-binding-lock:" + suffix
	if err := storeA.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "gateway_managed_users" {
			return
		}
		pauseOnce.Do(func() {
			close(started)
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				_ = tx.AddError(context.DeadlineExceeded)
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storeA.db.Callback().Update().Remove(callbackName); err != nil {
			t.Errorf("remove managed OIDC callback: %v", err)
		}
	})

	firstDone := make(chan error, 1)
	go func() {
		resolved, err := storeA.ResolveGatewayManagedOIDCUser(fixtures[0].principal.ExternalPrincipalID, fixtures[0].tenant.ExternalTenantID, issuer, subject)
		if err == nil && resolved.ID != fixtures[0].user.ID {
			err = fmt.Errorf("first binding resolved user %q, want %q", resolved.ID, fixtures[0].user.ID)
		}
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first OIDC binding did not reach the commit barrier")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := storeB.ResolveGatewayManagedOIDCUser(fixtures[1].principal.ExternalPrincipalID, fixtures[1].tenant.ExternalTenantID, issuer, subject)
		secondDone <- err
	}()
	var secondErr error
	secondFinishedEarly := false
	select {
	case secondErr = <-secondDone:
		secondFinishedEarly = true
	case <-time.After(200 * time.Millisecond):
	}

	releaseBinding()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if secondFinishedEarly {
		t.Fatalf("competing OIDC binding bypassed the database lock: %v", secondErr)
	}
	secondErr = <-secondDone
	if AsHTTPError(secondErr).Code != "gateway_identity_conflict" {
		t.Fatalf("competing OIDC binding returned %v", secondErr)
	}
}
