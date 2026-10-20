import type { Provider } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";

test("providers local-provider-labels", async ({ page, api }, testInfo) => {
  const providers: Provider[] = [
    { id: "prv_ui_local", name: "UI Local Cluster", type: "mock", base_url: "", priority: 1, status: "active", healthy: true },
    { id: "prv_ui_internal", name: "UI Internal Cluster", type: "mock", base_url: "http://inference.example.test/v1", priority: 2, status: "active", healthy: true },
  ];
  api.respond("GET", "/api/admin/providers", { data: providers });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "local", name: "Mock Catalog", display_name: "Mock Provider", type: "mock", models_count: 0, source: "plugin" }] });
  api.respond("GET", "/api/admin/provider-catalog/local", { data: { id: "local", name: "Local Cluster", type: "mock", models_count: 0, models: [], source: "plugin" } });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "mock", capabilities: ["chat"], plugin_id: "tokenhub.provider.mock" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.mock", name: "Mock Plugin", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "mock" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.define("GET", "/api/admin/audit/requests", () => ({ json: {
    data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
    summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
  } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });

  await page.goto("/providers");
  const local = page.getByRole("row").filter({ hasText: "UI Local Cluster" });
  const internal = page.getByRole("row").filter({ hasText: "UI Internal Cluster" });
  await expect(local.getByText("本地服务 · 本地服务", { exact: true })).toBeVisible();
  await expect(internal.getByText("本地服务 · http://inference.example.test/v1", { exact: true })).toBeVisible();
  await expect(page.locator(".provider-channel-list")).not.toContainText(/mock/i);
  await capture(page, testInfo, page.locator(".provider-channel-list"), "providers-local-labels", "本地服务类型与地址显示");

  const providerReads = () => api.calls.filter(call => call.path === "/api/admin/providers").length;
  const initialProviderReads = providerReads();
  for (const locale of [
    { language: "en", option: "English", label: "Local Provider", edit: "Edit", advanced: "Advanced", type: "Provider Type", close: "Close" },
    { language: "ja", option: "日本語", label: "ローカルサービス", edit: "編集", advanced: "詳細", type: "Provider タイプ", close: "閉じる" },
    { language: "zh-CN", option: "简体中文", label: "本地服务", edit: "编辑", advanced: "高级", type: "渠道商类型", close: "关闭" },
  ]) {
    await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
    await page.getByRole("option", { name: locale.option, exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    await expect(local.getByText(`${locale.label} · ${locale.label}`, { exact: true })).toBeVisible();
    await local.getByRole("button", { name: locale.edit, exact: true }).click();
    const editor = page.locator(".provider-modal");
    await editor.getByRole("tab", { name: locale.advanced, exact: true }).click();
    const providerType = editor.getByRole("combobox", { name: locale.type, exact: true });
    await expect(providerType).toHaveValue("mock");
    await expect(providerType.locator("option:checked")).toHaveText(locale.label);
    await capture(page, testInfo, editor, `providers-local-labels-${locale.language}`, "切换语言后的本地服务类型");
    await editor.getByTitle(locale.close, { exact: true }).click();
    expect(providerReads(), "Language changes must not reload provider data").toBe(initialProviderReads);
  }
});

test("providers custom model directory renders the complete response", async ({ page, api }, testInfo) => {
  const provider: Provider = {
    id: "prv_ui_custom", name: "UI Custom Provider", type: "openai_compatible",
    base_url: "https://provider.example.test/v1", priority: 1, status: "active", healthy: true,
    options: { catalog_id: "custom", model_category: "openai" },
  };
  const models = Array.from({ length: 100 }, (_, index) => ({
    id: `ui-upstream-${String(index + 1).padStart(3, "0")}`,
    name: `ui-upstream-${String(index + 1).padStart(3, "0")}`,
    display_name: `UI Upstream Model ${index + 1}`,
    category: index < 50 ? "openai" : "custom",
  }));
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "custom", name: "Custom", display_name: "Custom", type: "openai_compatible", categories: ["openai", "custom"], models_count: 100 }] });
  api.respond("POST", "/api/admin/provider-catalog/custom", { data: { id: "custom", name: "Custom", display_name: "Custom", type: "openai_compatible", categories: ["openai", "custom"], models_count: 100, models } });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "openai_compatible", capabilities: ["chat"] }] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions", "plugins", "plugin-marketplace", "plugin-background-jobs"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.define("GET", "/api/admin/audit/requests", () => ({ json: {
    data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
    summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
  } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });

  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑" }).click();
  const editor = page.locator(".provider-modal");
  await editor.getByRole("tab", { name: "模型" }).click();
  await expect(editor.getByText("100/100 个可引入模型")).toBeVisible();
  await expect(editor.locator(".model-option")).toHaveCount(100);
  await expect(editor.locator(".pagination")).toHaveCount(0);
  await capture(page, testInfo, editor, "providers-custom-models-list", "Provider 模型目录完整列表", "viewport");
  await editor.getByText("UI Upstream Model 100").scrollIntoViewIfNeeded();
  await expect(editor.getByText("UI Upstream Model 100")).toBeVisible();
  await capture(page, testInfo, editor, "providers-custom-models-end", "Provider 模型目录末项", "viewport");
  await page.setViewportSize({ width: 390, height: 844 });
  await editor.getByText("UI Upstream Model 1", { exact: true }).scrollIntoViewIfNeeded();
  await capture(page, testInfo, editor, "providers-custom-models-mobile", "Provider 模型目录手机视口", "viewport");
});
