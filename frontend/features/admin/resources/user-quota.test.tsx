import { afterEach, describe, expect, it, vi } from "vitest";
import { loadUserQuotaSnapshot } from "./user-quota";

const api = { baseURL: "https://tokenhub.example", adminToken: "admin-token" };
const quota = {
  user_id: "user-1",
  policy_configured: true,
  limits: { rate_limit_rpm: 20, token_limit_tpm: 800, daily_requests: 100, monthly_requests: 1000, monthly_tokens: 5000, monthly_cost_usd: 12, max_concurrency: 3 },
  usage: {
    daily: { requests: 1, prompt_tokens: 2, completion_tokens: 3, total_tokens: 5, cost_usd: 0.1 },
    monthly: { requests: 4, prompt_tokens: 5, completion_tokens: 6, total_tokens: 11, cost_usd: 0.2 },
  },
};

afterEach(() => vi.unstubAllGlobals());

describe("loadUserQuotaSnapshot", () => {
  it("loads the preferred effective tenant explicitly", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ data: [{ id: "tenant-a", name: "Tenant A" }, { id: "tenant-b", name: "Tenant B" }] }))
      .mockResolvedValueOnce(Response.json(quota));
    vi.stubGlobal("fetch", fetchMock);

    const result = await loadUserQuotaSnapshot(api, "tenant-b");

    expect(fetchMock.mock.calls[1][0]).toBe("https://tokenhub.example/api/admin/usage/quota?tenant_id=tenant-b");
    expect(result.tenant_id).toBe("tenant-b");
    expect(result.tenants).toHaveLength(2);
  });

  it("falls back to the first effective tenant", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ data: [{ id: "tenant-a", name: "Tenant A" }] }))
      .mockResolvedValueOnce(Response.json({ ...quota, tenant_id: "tenant-a" }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await loadUserQuotaSnapshot(api, "tenant-outside-scope");

    expect(fetchMock.mock.calls[1][0]).toBe("https://tokenhub.example/api/admin/usage/quota?tenant_id=tenant-a");
    expect(result.tenant_id).toBe("tenant-a");
  });
});
