# End-to-end tests with Terragrunt

This suite runs the provider against a real Plerion API. It builds the provider from this checkout, points Terraform at the build with a development override, and applies a small Terragrunt stack in several phases.

## Running it

```sh
PLERION_API_URL=https://<api-address-of-your-test-tenant> \
PLERION_API_KEY_FILE=~/path/to/key \
tests/terragrunt/run.sh
```

You need `go`, `terraform`, `terragrunt`, `jq` and `curl` on your `PATH`. A full run takes about two minutes.

| Variable | Purpose |
| --- | --- |
| `PLERION_API_URL` | Required. The API address of the test environment. |
| `PLERION_API_KEY` or `PLERION_API_KEY_FILE` | Required. A tenant API key, or a file that holds one. The key needs permission to manage custom checks and to read assets. |
| `PLERION_TEST_INTEGRATION_ID` | Optional. The integration dry runs use. When it is not set, the suite picks the tenant's first active AWS integration, and skips dry runs if there is none. |
| `PLERION_TEST_ALLOW_PRODUCTION` | Optional. The suite refuses to run against a production address unless this is `true`. |

## What it covers

The stack has four units under `units/`:

| Unit | What it does |
| --- | --- |
| `checks` | Two `plerion_custom_check` resources: one with only the required attributes, and one that sets everything. |
| `lookups` | Looks the full check up by ID and by slug, and lists the checks for one asset type. |
| `dry-run` | Previews a policy against a real integration with `plerion_custom_check_dry_run`. |
| `conflict` | Tries to create a check with a slug that is already taken. It is excluded from the stack, and the suite applies it on its own, expecting it to fail. |

`run.sh` goes through these steps and checks Terraform's outputs and the API itself after each one:

1. Create the checks, and confirm that Plerion stored every attribute and that the data sources return the same values.
2. Plan again and expect no changes, which catches any attribute the provider reads back differently from how it was written.
3. Apply the update phase, which renames the full check's slug, changes most of its values and removes every optional attribute. Confirm that Plerion removed them too.
4. Edit a check through the API, and expect the plan to show the change and the apply to undo it.
5. Delete a check through the API, and expect the apply to create it again.
6. Remove a check from state and import it by its slug.
7. Apply the `conflict` unit, and expect it to fail with the slug conflict message.
8. Destroy the stack, and confirm that no check from the run is left in Plerion.

## Safety

Every slug starts with a run ID such as `tg-20260929101500-1234`, so a run only touches the checks it created. When the suite exits, including after a failure or Ctrl-C, it destroys the stack and then deletes any check that still has the run's prefix.

Creating a check makes Plerion run it against the tenant's integrations straight away. The suite's policies never report an asset, so a run produces no findings. The dry-run policy does flag every asset, but dry-run findings are never saved.

Logs for each step are written to `.logs/` in this directory, and the suite prints the tail of the log for any step that fails.
