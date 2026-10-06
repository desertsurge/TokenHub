import { describe, expect, it } from "vitest";
import { resourceConfigFor } from "./settings-config";

describe("quota policy configuration", () => {
  it("exposes the tenant scope for user policies and hides it for other scopes", () => {
    const config = resourceConfigFor("quota-policies");
    if (!config) throw new Error("expected quota policy configuration");
    const tenantField = config.fields.find((field) => field.key === "tenant_external_id");
    if (!tenantField?.visible) throw new Error("expected tenant scope field");

    expect(tenantField.visible({ scope: "user" })).toBe(true);
    expect(tenantField.visible({ scope: "project" })).toBe(false);
  });

  it("round-trips the tenant scope when editing a policy", () => {
    const config = resourceConfigFor("quota-policies");
    if (!config) throw new Error("expected quota policy configuration");
    const toForm = config.toForm;
    if (!toForm) throw new Error("expected quota policy form mapper");

    expect(toForm({
      id: "quota-tenant-user",
      name: "Tenant user quota",
      description: "",
      status: "active",
      fields: { scope: "user", scope_id: "usr_1", tenant_external_id: "tenant_1" },
    } as never).tenant_external_id).toBe("tenant_1");
  });
});
