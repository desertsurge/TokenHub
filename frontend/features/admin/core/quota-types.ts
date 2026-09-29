export type UserQuotaCounter = {
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cost_usd: number;
};

export type UserQuotaSnapshot = {
  user_id: string;
  policy_configured: boolean;
  limits: {
    rate_limit_rpm: number;
    token_limit_tpm: number;
    daily_requests: number;
    monthly_requests: number;
    daily_tokens: number;
    monthly_tokens: number;
    daily_cost_usd: number;
    monthly_cost_usd: number;
    max_concurrency: number;
  };
  usage: {
    minute: UserQuotaCounter;
    daily: UserQuotaCounter;
    monthly: UserQuotaCounter;
  };
};
