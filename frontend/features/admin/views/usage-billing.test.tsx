import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { DailyUsageSection } from "./usage-billing";
import { PersonalQuotaSummary } from "./personal-quota-summary";

describe("DailyUsageSection", () => {
  it("shows mutually exclusive daily token type rows", () => {
    const data = emptyData();
    data.dailyUsage.summary = {
      ...data.dailyUsage.summary,
      input_tokens: 100,
      cached_input_tokens: 20,
      cache_write_input_tokens: 5,
      output_tokens: 50,
      total_tokens: 150,
    };

    render(<DailyUsageSection data={data} user={{ id: "usr_admin", username: "admin", name: "Admin", email: "admin@example.test", role: "admin", status: "active" }} />);

    const table = screen.getByRole("heading", { name: "今日 Token 类型" }).closest("article");
    expect(table).not.toBeNull();
    expect(within(table as HTMLElement).getByRole("row", { name: "输入 75" })).toBeInTheDocument();
    expect(within(table as HTMLElement).getByRole("row", { name: "缓存读 20" })).toBeInTheDocument();
    expect(within(table as HTMLElement).getByRole("row", { name: "缓存写 5" })).toBeInTheDocument();
    expect(within(table as HTMLElement).getByRole("row", { name: "输出 50" })).toBeInTheDocument();
  });

  it("hides provider daily breakdowns from team leaders", () => {
    const data = emptyData();
    data.dailyUsage.breakdown.providers = [{
      id: "provider_sensitive",
      request_count: 1,
      input_tokens: 10,
      cached_input_tokens: 0,
      output_tokens: 5,
      total_tokens: 15,
      estimated_cost_usd: 12.34,
    }];
    data.dailyUsage.breakdown.provider_resources = [{
      id: "resource_sensitive",
      request_count: 1,
      input_tokens: 10,
      cached_input_tokens: 0,
      output_tokens: 5,
      total_tokens: 15,
      estimated_cost_usd: 12.34,
    }];

    render(<DailyUsageSection data={data} user={{ id: "usr_leader", username: "leader", name: "Leader", email: "leader@example.test", role: "team_leader", status: "active" }} />);

    expect(screen.queryByRole("heading", { name: "今日 Provider 用量" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "今日资源账号用量" })).not.toBeInTheDocument();
    expect(screen.queryByText("provider_sensitive")).not.toBeInTheDocument();
    expect(screen.queryByText("resource_sensitive")).not.toBeInTheDocument();
  });
});

describe("PersonalQuotaSummary", () => {
  it("shows the authenticated user's aggregate quota and usage", () => {
    const data = emptyData();
    data.userQuota = {
      user_id: "usr_quota",
      policy_configured: true,
      limits: {
        rate_limit_rpm: 20,
        token_limit_tpm: 800,
        daily_requests: 100,
        monthly_requests: 1000,
        daily_tokens: 800,
        monthly_tokens: 8000,
        daily_cost_usd: 10,
        monthly_cost_usd: 100,
        max_concurrency: 3,
      },
      usage: {
        minute: { requests: 2, prompt_tokens: 120, completion_tokens: 80, total_tokens: 200, cost_usd: 0.2 },
        daily: { requests: 25, prompt_tokens: 240, completion_tokens: 160, total_tokens: 400, cost_usd: 2.5 },
        monthly: { requests: 100, prompt_tokens: 1500, completion_tokens: 500, total_tokens: 2000, cost_usd: 12.5 },
      },
    };

    render(<PersonalQuotaSummary quota={data.userQuota} />);

    expect(screen.getByRole("heading", { name: "当前用户配额" })).toBeInTheDocument();
    expect(screen.getByText("已配置用户聚合策略")).toBeInTheDocument();
    const minuteCard = screen.getByText("本分钟 Token").closest("article");
    expect(minuteCard).not.toBeNull();
    expect(within(minuteCard as HTMLElement).getByText("200")).toBeInTheDocument();
    expect(within(minuteCard as HTMLElement).getByText("上限 800")).toBeInTheDocument();
    expect(screen.getByText("RPM 20")).toBeInTheDocument();
    expect(screen.getByText("TPM 800")).toBeInTheDocument();
    expect(screen.getByText("最大并发 3")).toBeInTheDocument();
    expect(screen.getByText("本月成本")).toBeInTheDocument();
  });
});
