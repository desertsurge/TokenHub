import type { UsageDaily } from "../../features/admin/core/types";
import type { UserQuotaSnapshot } from "../../features/admin/core/quota-types";
import { test, expect, capture } from "./harness";
import { user } from "./fixtures/shell";
import type { MockAPI } from "./network";

function setupUsage(api: MockAPI) {
  const counter = { requests: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, cost_usd: 0 };
  const daily: UsageDaily = {
    timezone: "UTC",
    date: "2026-09-07",
    window_start: "2026-09-07T00:00:00.000Z",
    window_end: "2026-09-08T00:00:00.000Z",
    summary: { request_count: 25, input_tokens: 240, cached_input_tokens: 0, output_tokens: 160, total_tokens: 400, estimated_cost_usd: 2.5, errors: 0 },
    breakdown: { projects: [], models: [], members: [], providers: [], provider_resources: [], cost_centers: [], api_keys: [] },
  };
  const quota: UserQuotaSnapshot = {
    user_id: user.id,
    policy_configured: true,
    limits: {
      rate_limit_rpm: 20, token_limit_tpm: 800, daily_requests: 100, monthly_requests: 1000,
      daily_tokens: 800, monthly_tokens: 8000, daily_cost_usd: 10, monthly_cost_usd: 100, max_concurrency: 3,
    },
    usage: {
      minute: { ...counter, requests: 2, total_tokens: 200 },
      daily: { ...counter, requests: 25, total_tokens: 400, cost_usd: 2.5 },
      monthly: { ...counter, requests: 100, total_tokens: 2000, cost_usd: 12.5 },
    },
  };

  api.respond("GET", "/api/admin/api-keys", { data: [] });
  api.respond("GET", "/api/admin/usage/daily", daily);
  api.respond("GET", "/api/admin/usage/quota", quota);
  api.respond("GET", "/api/admin/usage/timeseries", { data: [] });
  api.respond("GET", "/api/admin/users", { data: [user] });
  api.respond("GET", "/api/admin/plugin-ui-manifest", { data: [] });
  api.respond("GET", "/api/admin/plugin-actions", { data: [] });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.respond("GET", "/api/admin/resources/teams", { data: [] });
  api.respond("GET", "/api/admin/resources/cost-centers", { data: [] });
}

test("usage current-user quota is visible to the signed-in administrator", async ({ page, api }, testInfo) => {
  setupUsage(api);
  await page.goto("/usage");

  const quota = page.locator("section.personal-quota-report");
  await expect(quota.getByRole("heading", { name: "当前用户配额" })).toBeVisible();
  await expect(quota.getByText("已配置用户聚合策略")).toBeVisible();
  await expect(quota.getByText("本分钟 Token")).toBeVisible();
  await expect(quota.getByText("本月成本")).toBeVisible();
  await expect(quota.getByText("RPM 20")).toBeVisible();
  await expect(quota.getByText("TPM 800")).toBeVisible();
  await expect(quota.getByText("最大并发 3")).toBeVisible();
  expect(api.calls.filter(call => call.path === "/api/admin/usage/quota")).toHaveLength(1);
  await capture(page, testInfo, quota, "usage-current-user-quota", "用量统计：当前用户配额");
});
