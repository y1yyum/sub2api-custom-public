# Gin mode fixture ordering

The first actual isolated PR5 integration artifact reports writes racing in
`gin.SetMode` from parallel tests in `openai_compat_model_test.go`. This is a
process-global test setup mutation, not a production forwarding change.

The adapter moves constant `gin.TestMode` setup **before** the top-level
`t.Parallel()` barrier. Go's top-level parallel tests resume after sequential
setup/tests finish; the request and assertion bodies still execute in parallel.
No global test lock, reduced race detection, skipped test, or new sleep is used.

Audit of upstream `.18` test functions found these direct parallel writers:

| Restored file | Top-level tests |
| --- | ---: |
| handler/payment_handler_resume_test.go | 1 |
| service/account_test_service_gemini_test.go | 1 |
| service/gateway_forward_as_chat_completions_test.go | 5 |
| service/gateway_forward_as_responses_test.go | 7 |
| service/openai_compat_model_test.go | 27 |
| service/openai_fast_service_tier_test.go | 1 |

The fast-tier test has three identical mode writes in nonparallel subtests of a
parallel parent. These become one pre-barrier setup call. The Gemini test also
calls `newTestContext` in `account_test_service_openai_test.go`; that helper's
identical global write moves to file `init`, before any test executes. Its
context/recorder creation remains unchanged and concurrent. The audit checked
same-package named helper calls transitively; this is a source-level audit, not
a claim of complete dynamic call-graph analysis.

All seven input files are SHA-256 pinned at both `.16` and `.18`. Static tests
verify unchanged test/Parallel/skip/sleep counts and byte-identical nonblank
content after removing only mode setup and the documented helper-init insertion.
This includes all original assertions. No backend or Docker execution occurred
while preparing this patch. Passing race validation still requires the reviewed
isolated CI environment; do not interpret static checks as runtime success.
