## 0.1.0 (Unreleased)

FEATURES:

* **New resource:** `plerion_custom_check` manages a custom check, a rego policy that Plerion evaluates against the assets it collects from your integrations. Import a check by its ID or by its slug.
* **New data source:** `plerion_custom_check` looks up one custom check by its ID or its slug, including a summary of its latest run.
* **New data source:** `plerion_custom_checks` lists the custom checks in the tenant, optionally filtered by asset type or scope.
* **New data source:** `plerion_custom_check_dry_run` runs a policy against one integration and returns the findings it would produce, without saving them. Reading it fails when the policy cannot be evaluated, so a broken policy stops the plan.

NOTES:

* The provider reads its API key from `api_key` or the `PLERION_API_KEY` environment variable, and its regional API address from `api_url` or `PLERION_API_URL`. The address defaults to `https://au.api.plerion.com`.
* Requests are retried when the API asks the provider to slow down, and on server errors for reads, updates and deletes. A failed create is not retried, because that could create the check twice.
* `defaults.meta` is compared by value, the way Plerion stores it. Plerion keeps numbers as 64-bit floating point values, so it may return `0.0000005` as `5e-7`, and neither form shows up as a change.
* `defaults.meta` must be a JSON object. Whole numbers larger than 9,007,199,254,740,991 are rejected during the plan, because Plerion cannot store them exactly. Store large identifiers as strings.
