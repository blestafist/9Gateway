package config

import "github.com/pestit/9gateway/internal/accounting"

// PricingConfig is the validated pricing representation used by both config
// loading and accounting resolution. Keeping the type in accounting prevents
// the resolver from accepting an ad-hoc raw rule table through a config-only
// adapter.
type PricingConfig = accounting.PricingConfig

type PricingRule = accounting.PricingRule
