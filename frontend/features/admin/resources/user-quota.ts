import { type UserQuotaSnapshot, type UserQuotaTenant } from "../core/quota-types";
import { type ApiContext } from "../core/types";
import { adminFetch, readLoadError } from "./payloads";

export async function loadUserQuotaSnapshot(ctx: ApiContext, preferredTenantID = ""): Promise<UserQuotaSnapshot> {
  const tenantsResponse = await adminFetch(ctx, "/api/admin/usage/quota/tenants");
  if (!tenantsResponse.ok) throw new Error(await readLoadError(tenantsResponse, "user-quota-tenants"));

  const tenantsPayload = (await tenantsResponse.json()) as { data?: UserQuotaTenant[] };
  const tenants = tenantsPayload.data ?? [];
  const tenantID = tenants.some((tenant) => tenant.id === preferredTenantID)
    ? preferredTenantID
    : tenants[0]?.id ?? "";
  const quotaPath = tenantID
    ? `/api/admin/usage/quota?tenant_id=${encodeURIComponent(tenantID)}`
    : "/api/admin/usage/quota";
  const quotaResponse = await adminFetch(ctx, quotaPath);
  if (!quotaResponse.ok) throw new Error(await readLoadError(quotaResponse, "user-quota"));

  const quota = (await quotaResponse.json()) as UserQuotaSnapshot;
  return { ...quota, tenant_id: quota.tenant_id ?? tenantID, tenants };
}
