package server

import (
	"net/http"
	"strings"
)

func (s *Server) validateIdentityProviderMutation(kind string, resourceID string, request AdminResource) error {
	if kind != "identity-providers" {
		return nil
	}

	effective := request
	if resourceID == "" {
		if effective.Status == "" {
			effective.Status = StatusActive
		}
	} else {
		existing, err := s.findResource(kind, resourceID)
		if err != nil {
			return err
		}
		if effective.Status == "" {
			effective.Status = existing.Status
		}
		if effective.Fields == nil {
			effective.Fields = existing.Fields
		}
	}

	if !strings.EqualFold(strings.TrimSpace(effective.Status), StatusActive) ||
		!strings.EqualFold(stringField(effective.Fields, "gateway_managed"), "true") {
		return nil
	}
	if validGatewayManagedProvider(effective) {
		return nil
	}
	return NewHTTPError(http.StatusBadRequest, "gateway_identity_provider_invalid", "Active managed gateway identity provider requires OIDC, trusted URLs, an OpenID scope, and explicit principal and tenant claims")
}
