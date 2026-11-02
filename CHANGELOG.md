## 0.2.0 (Unreleased)

FEATURES:

* **New resource:** `plerion_aws_integration` connects an AWS account to Plerion. Plerion assumes the role you give it, with your tenant's external ID, before it saves anything. Import an integration by its ID.
* **New resource:** `plerion_aws_service_account` sets up an AWS account to run Plerion's workload scanners in. Import it by its AWS account ID.
* **New resource:** `plerion_aws_service_account_region` sets the network a service account's scanners use in one AWS region. Plerion checks that the VPC is tagged with your tenant ID and that the subnet, security group and queue belong to it. Import it by `<account ID>/<region>`.
* **New data source:** `plerion_tenant` reads your tenant ID and the external ID your IAM roles trust Plerion with.

ENHANCEMENTS:

* `api_url` and `PLERION_API_URL` accept the host on its own, such as `au.api.plerion.com`, and read it as https. Plerion's API examples set `PLERION_API_URL` that way.

BUG FIXES:

* An error that passes on Plerion's own message no longer ends in two full stops.

NOTES:

* An account from an AWS organization your tenant hasn't connected before is held until someone approves it in the Plerion console. `terraform apply` doesn't wait: the resource is created with `status = "pending_approval"` and Terraform prints a warning.
* A role created in the same apply can take a minute or two before AWS lets Plerion assume it. The provider retries for up to two minutes.
* Turning a service account or a region on and off stays in the Plerion console, so `enabled` is read-only.
* `terraform destroy` removes integrations, service accounts and regions from Plerion.

## 0.1.0 (October 29, 2026)

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
