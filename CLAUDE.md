# terraform-provider-plerion

Terraform provider (`plerionhq/plerion`) for the Plerion customer API. It manages custom checks as code: the `plerion_custom_check` resource, lookup and list data sources, and a dry-run data source that previews findings during a plan. It calls only the public `/v1/tenant/custom-checks` and `/v1/tenant/custom-check-dry-runs` routes.

## Debugging and gotchas

- **An update is a full replacement.** `PUT /v1/tenant/custom-checks/{id}` removes any optional field the body leaves out, so the resource always sends the whole check. A PUT that changes nothing keeps the version unchanged.
- **The API rewrites `defaults.meta`.** It returns keys in a different order and numbers as 64-bit floats: `0.0000005` comes back as `5e-7`, and `0.12345678901234567` as `0.12345678901234566`. `metaType` compares the JSON by value, and `flattenMeta` writes what the API returns in the form `jsonencode()` produces (sorted keys, numbers in full). Without both, applies fail with "inconsistent result" and imports show a diff. `jsontypes.Normalized` alone compares number text, so it is not enough.
- **Whole numbers above 2^53 cannot be stored exactly.** `jsonObjectValidator` rejects them in `meta` during the plan, so they never reach the API.
- **There is no slug filter.** Slug lookups, both import by slug and the `plerion_custom_check` data source with `slug`, page through the whole list.
- **Keys are regional.** A key created in one region gets 401 from every other region's API address, so the provider's 401 message says to check `api_url`.
- **Dry runs are read at plan time only when nothing they refer to is changing.** If the data source refers to a `plerion_custom_check` resource with changes, Terraform defers it to apply, after the check has already changed. The docs and `TestCustomCheckDryRunGatesTheApply` show the working pattern, which shares values through `locals`.
- **Creating a check runs it for real.** Plerion runs a new or changed check against every matching integration straight away. Test policies, in both the provider tests and `tests/terragrunt`, must never report an asset.
- **`docs/` is generated.** Edit schema descriptions, `templates/` or `examples/`, then run `make docs`. `make lint` fails when `docs/` is out of date.
- **The HTTP client's timeout reports `context.DeadlineExceeded`.** `shouldRetry` and the dry-run timeout message check the caller's context instead, or a slow response looks like the caller giving up.
- **`internal/fakeapi` mirrors the real API.** When the API changes behavior, change the fake in the same pull request, or the provider tests keep passing against behavior that no longer exists.

## Docs

- `README.md`: building, testing, documentation and releasing.
- `docs/`: generated user documentation, published to the Terraform Registry (do not hand-edit).
- `tests/terragrunt/README.md`: the end-to-end suite and how to run it.
- `CHANGELOG.md`: release notes for customers.
