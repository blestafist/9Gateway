package accounting

// EstimateQuality describes whether an estimator produced a usable input
// token count. Unknown is distinct from a known count of zero.
type EstimateQuality uint8

const (
	EstimateQualityUnknown EstimateQuality = iota
	EstimateQualityKnown
)

const (
	EstimateUnknown = EstimateQualityUnknown
	EstimateKnown   = EstimateQualityKnown
)

// Known reports whether an estimator supplied a usable count.
func (quality EstimateQuality) Known() bool { return quality == EstimateQualityKnown }

// TokenEstimator is the narrow accounting contract for preflight estimation.
// The caller must provide model and requestBytes already bounded according to
// deployment configuration. Implementations must not read HTTP bodies, access
// storage or depend on policy and pricing packages.
type TokenEstimator interface {
	EstimateInputTokens(model string, requestBytes []byte) (InputTokens, EstimateQuality, error)
}

// InputEstimator is a descriptive alias for the preflight input estimator
// contract.
type InputEstimator = TokenEstimator
