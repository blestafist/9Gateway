# Accounting

## Usage And Estimation

Actual upstream `usage` is the source of truth when available. Internal usage
tracks input, output, total, cached, and reasoning tokens as available. Missing
usage is an explicit unknown state, not automatically an error or zero.

Preflight estimation supports `usage_only`, approximate estimation, and later
model-specific exact tokenizers. Estimator failure does not reject a request
unless strict enforcement requires an estimate. Approximation includes textual
messages and tool schemas; each UTF-8 byte costs one token, with fixed
structural overhead for messages and tools. This model-independent rule is
intentionally conservative for common BPE tokenizers; multimodal/unknown input
uses a configured fallback.

## Token Reservation

Before upstream work, reserve estimated input plus potential output. A request
passes only if committed usage plus active reservations plus the new reservation
fits every configured token window. On completion remove the reservation and
commit actual usage; refund the difference. On ambiguous failure, use the
documented conservative policy and never leave a reservation forever.

## Pricing

Never use `float64` for money. Store integer micro-units or an exact decimal.
Pricing rules use model exact/glob matching and separate input/output prices per
million tokens. Missing pricing remains `unknown`; it is not silently `$0`.

## Budget

Budget state separates spent, reserved, and available amounts. Reserve estimated
cost before concurrent work, reject when spent plus reservations would exceed
the limit, and reconcile against actual usage. Start with total budget, then add
daily and calendar-month periods. Budget calculations never control response
protocol behavior or block per-chunk delivery.

## Mid-Stream Enforcement

MVP uses strict pre-request reservation and post-request reconciliation. Do not
interrupt streams using approximate per-chunk tokenization. Administrative kill
is a separate exception: cancel upstream, stop transport, preserve known partial
usage, and do not inject ordinary JSON into an already-started SSE response.
