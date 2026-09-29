import { type UserQuotaSnapshot } from "../core/quota-types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";

export function PersonalQuotaSummary({ quota }: { quota: UserQuotaSnapshot }) {
  const limits = quota.limits;
  const usage = quota.usage;
  return (
    <section className="executive-report personal-quota-report">
      <header className="executive-report-head">
        <div>
          <p className="eyebrow">{tx("我的额度")}</p>
          <h2>{tx("当前用户配额")}</h2>
        </div>
        <div className="executive-report-tools">
          <span>{quota.policy_configured ? tx("已配置用户聚合策略") : tx("未配置用户聚合策略")}</span>
          <span>{tx("按当前用户归属统计")}</span>
        </div>
      </header>
      <div className="api-key-quota-grid">
        <PersonalQuotaCard label="本分钟 Token" used={usage.minute.total_tokens} limit={limits.token_limit_tpm} tokens />
        <PersonalQuotaCard label="今日 Token" used={usage.daily.total_tokens} limit={limits.daily_tokens} tokens />
        <PersonalQuotaCard label="本月 Token" used={usage.monthly.total_tokens} limit={limits.monthly_tokens} tokens />
        <PersonalQuotaCard label="今日请求" used={usage.daily.requests} limit={limits.daily_requests} />
        <PersonalQuotaCard label="本月请求" used={usage.monthly.requests} limit={limits.monthly_requests} />
        <PersonalQuotaCard label="今日成本" used={usage.daily.cost_usd} limit={limits.daily_cost_usd} money />
        <PersonalQuotaCard label="本月成本" used={usage.monthly.cost_usd} limit={limits.monthly_cost_usd} money />
      </div>
      <div className="api-key-effective-limits">
        <span>{formatTranslationTemplate(tx("RPM {limit}"), { limit: quotaLimitText(limits.rate_limit_rpm) })}</span>
        <span>{formatTranslationTemplate(tx("TPM {limit}"), { limit: quotaLimitText(limits.token_limit_tpm, true) })}</span>
        <span>{formatTranslationTemplate(tx("最大并发 {limit}"), { limit: quotaLimitText(limits.max_concurrency) })}</span>
      </div>
    </section>
  );
}

function PersonalQuotaCard({ label, used, limit, money = false, tokens = false }: { label: string; used: number; limit: number; money?: boolean; tokens?: boolean }) {
  const percent = limit > 0 ? Math.max(0, used / limit * 100) : 0;
  const value = money ? `$${formatMoney(used)}` : tokens ? compactNumber(used) : formatNumber(used);
  const limitValue = limit > 0 ? (money ? `$${formatMoney(limit)}` : tokens ? compactNumber(limit) : formatNumber(limit)) : tx("不限");
  return (
    <article className="api-key-quota-card">
      <span>{tx(label)}</span><strong>{value}</strong><small>{formatTranslationTemplate(tx("上限 {limit}"), { limit: limitValue })}</small>
      {limit > 0 ? <div className="api-key-quota-track"><span style={{ width: `${Math.min(100, percent)}%` }} /></div> : null}
      {limit > 0 ? <em>{new Intl.NumberFormat(languageLocale(), { style: "percent", maximumFractionDigits: 1 }).format(percent / 100)}</em> : null}
    </article>
  );
}

function quotaLimitText(value: number, tokens = false) {
  if (!value || value <= 0) return tx("不限");
  return tokens ? compactNumber(value) : formatNumber(value);
}
