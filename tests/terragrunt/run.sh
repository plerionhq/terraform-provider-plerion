#!/usr/bin/env bash
# End-to-end tests for the provider against a real Plerion API, driven by
# Terragrunt. See README.md in this directory for what each step covers.
set -euo pipefail

SUITE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SUITE_DIR/../.." && pwd)"
UNITS_DIR="$SUITE_DIR/units"
LOG_DIR="$SUITE_DIR/.logs"

# Configuration.

if [[ -z "${PLERION_API_KEY:-}" && -n "${PLERION_API_KEY_FILE:-}" ]]; then
  PLERION_API_KEY="$(tr -d '[:space:]' < "$PLERION_API_KEY_FILE")"
fi
if [[ -z "${PLERION_API_URL:-}" || -z "${PLERION_API_KEY:-}" ]]; then
  echo "Set PLERION_API_URL, and PLERION_API_KEY or PLERION_API_KEY_FILE. See tests/terragrunt/README.md." >&2
  exit 2
fi
PLERION_API_URL="${PLERION_API_URL%/}"
if [[ "$PLERION_API_URL" == *".api.plerion.com"* && "${PLERION_TEST_ALLOW_PRODUCTION:-}" != "true" ]]; then
  echo "Refusing to run against the production API at $PLERION_API_URL." >&2
  echo "The suite creates and deletes custom checks. Set PLERION_TEST_ALLOW_PRODUCTION=true if you really mean it." >&2
  exit 2
fi
export PLERION_API_URL PLERION_API_KEY

for tool in go terraform terragrunt jq curl; do
  if ! command -v "$tool" > /dev/null; then
    echo "$tool is required but was not found on PATH." >&2
    exit 2
  fi
done

PLERION_TEST_RUN_ID="tg-$(date -u +%Y%m%d%H%M%S)-$RANDOM"
PLERION_TEST_PHASE="create"
export PLERION_TEST_RUN_ID PLERION_TEST_PHASE
export TG_TF_PATH="${TG_TF_PATH:-terraform}"
export TG_NON_INTERACTIVE=true TG_NO_AUTO_RETRY=true TG_NO_COLOR=true TG_LOG_LEVEL=error
export TF_IN_AUTOMATION=1 TF_INPUT=0

WORK_DIR="$(mktemp -d)"
rm -rf "$LOG_DIR"
mkdir -p "$LOG_DIR" "$WORK_DIR/bin"

# The key goes in a header file so it never appears in the process list.
AUTH_HEADER_FILE="$WORK_DIR/auth-header"
(umask 077 && printf 'Authorization: Bearer %s\n' "$PLERION_API_KEY" > "$AUTH_HEADER_FILE")

# Output helpers.

step_number=0
assertions=0

step() {
  step_number=$((step_number + 1))
  printf '\n== %d. %s\n' "$step_number" "$1"
}

pass() {
  assertions=$((assertions + 1))
  printf '  ok    %s\n' "$1"
}

fail() {
  printf '  FAIL  %s\n' "$1"
  exit 1
}

show_log() {
  sed -e 's/^/        /' "$1" | tail -n 40
}

expect_eq() {
  local actual="$1" expected="$2" description="$3"
  if [[ "$actual" != "$expected" ]]; then
    fail "$description: expected '$expected', got '$actual'"
  fi
  pass "$description"
}

expect_ne() {
  local actual="$1" unexpected="$2" description="$3"
  if [[ "$actual" == "$unexpected" ]]; then
    fail "$description: did not expect '$unexpected'"
  fi
  pass "$description"
}

# Terragrunt and API helpers.

tg_all() {
  terragrunt run --all --working-dir "$UNITS_DIR" -- "$@"
}

tg_unit() {
  local unit="$1"
  shift
  terragrunt run --working-dir "$UNITS_DIR/$unit" -- "$@"
}

run_logged() {
  local name="$1"
  shift
  local log="$LOG_DIR/$name.log"
  if ! "$@" > "$log" 2>&1; then
    show_log "$log"
    fail "$name failed. Full log: $log"
  fi
}

outputs() {
  tg_unit "$1" output -json 2> /dev/null
}

expect_plan_exit_code() {
  local unit="$1" expected="$2" description="$3"
  local log="$LOG_DIR/$step_number-plan-$unit.log"
  local code=0
  tg_unit "$unit" plan -detailed-exitcode -no-color > "$log" 2>&1 || code=$?
  if [[ "$code" != "$expected" ]]; then
    show_log "$log"
    fail "$description: plan exited $code, expected $expected. Full log: $log"
  fi
  pass "$description"
}

expect_no_changes() {
  expect_plan_exit_code "$1" 0 "$1: plan shows no changes"
}

expect_changes() {
  expect_plan_exit_code "$1" 2 "$1: plan shows changes"
}

api() {
  local method="$1" path="$2"
  shift 2
  curl -sS --fail-with-body -X "$method" "$PLERION_API_URL$path" \
    -H @"$AUTH_HEADER_FILE" -H "Content-Type: application/json" "$@"
}

api_status() {
  curl -sS -o /dev/null -w '%{http_code}' "$PLERION_API_URL$1" -H @"$AUTH_HEADER_FILE"
}

run_check_ids() {
  local cursor="" page
  while :; do
    page="$(api GET "/v1/tenant/custom-checks?limit=200${cursor:+&cursor=$cursor}")"
    jq -r --arg prefix "$PLERION_TEST_RUN_ID" '.items[] | select(.slug | startswith($prefix)) | .customCheckId' <<< "$page"
    cursor="$(jq -r '.nextCursor // empty | @uri' <<< "$page")"
    [[ -n "$cursor" ]] || break
  done
}

cleanup() {
  local code=$?
  trap - EXIT
  printf '\n== Cleaning up\n'
  if ! tg_all destroy > "$LOG_DIR/cleanup-destroy.log" 2>&1; then
    echo "  terragrunt destroy failed, so leftovers are deleted through the API. Log: $LOG_DIR/cleanup-destroy.log"
  fi
  local id
  for id in $(run_check_ids 2> /dev/null || true); do
    if api DELETE "/v1/tenant/custom-checks/$id" > /dev/null 2>&1; then
      echo "  deleted leftover check $id"
    else
      echo "  could not delete leftover check $id; delete it by hand"
    fi
  done
  find "$UNITS_DIR" \( -name .terragrunt-cache -o -name 'terraform.tfstate*' -o -name .terraform.lock.hcl \) \
    -prune -exec rm -rf {} +
  rm -rf "$WORK_DIR"

  if [[ "$code" == 0 ]]; then
    printf '\nAll %d assertions passed. Run ID: %s\n' "$assertions" "$PLERION_TEST_RUN_ID"
  else
    printf '\nThe suite failed after %d passing assertions. Run ID: %s. Logs: %s\n' "$assertions" "$PLERION_TEST_RUN_ID" "$LOG_DIR"
  fi
  exit "$code"
}
trap cleanup EXIT

# Setup.

echo "Plerion API:  $PLERION_API_URL"
echo "Run ID:       $PLERION_TEST_RUN_ID"

if [[ "$(api_status /v1/tenant/custom-checks?limit=1)" != 200 ]]; then
  fail "The API rejected the key or could not be reached. Check PLERION_API_URL and the API key."
fi

if [[ -z "${PLERION_TEST_INTEGRATION_ID:-}" ]]; then
  PLERION_TEST_INTEGRATION_ID="$(api GET /v1/tenant/integrations |
    jq -r '[(.data // .)[] | select(.provider == "AWS" and .status == "Active")][0].integrationId // empty')"
fi
export PLERION_TEST_INTEGRATION_ID
if [[ -n "$PLERION_TEST_INTEGRATION_ID" ]]; then
  echo "Dry runs use: $PLERION_TEST_INTEGRATION_ID"
  plan_units=(checks lookups dry-run)
else
  echo "Dry runs:     skipped, because the tenant has no active AWS integration"
  plan_units=(checks lookups)
fi

(cd "$REPO_ROOT" && go build -o "$WORK_DIR/bin/terraform-provider-plerion" .)
cat > "$WORK_DIR/terraformrc" << EOF
provider_installation {
  dev_overrides {
    "plerionhq/plerion" = "$WORK_DIR/bin"
  }
  direct {}
}
EOF
export TF_CLI_CONFIG_FILE="$WORK_DIR/terraformrc"

# Steps.

step "Create the checks"
run_logged create-apply tg_all apply
checks="$(outputs checks)"
MINIMAL_ID="$(jq -r .minimal.value.id <<< "$checks")"
MINIMAL_SLUG="$(jq -r .minimal.value.slug <<< "$checks")"
FULL_ID="$(jq -r .full.value.id <<< "$checks")"
FULL_SLUG="$(jq -r .full.value.slug <<< "$checks")"
expect_eq "$MINIMAL_SLUG" "$PLERION_TEST_RUN_ID-minimal" "minimal check has the configured slug"
expect_eq "$(jq -r .minimal.value.version <<< "$checks")" 1 "a new check starts at version 1"
expect_eq "$(jq -r .minimal.value.type <<< "$checks")" rego "type defaults to rego"
expect_eq "$(jq -r .minimal.value.defaults.informational <<< "$checks")" false "informational defaults to false"
expect_eq "$(jq -r '.minimal.value.description' <<< "$checks")" null "an unset description stays null"
expect_eq "$(jq -r '.full.value.additional_assets | map(.asset_type) | join(",")' <<< "$checks")" \
  "AWS::IAM::Role,AWS::KMS::Key" "additional assets keep their order"
expect_eq "$(jq -r '.full.value.defaults.meta | fromjson | .labels.team' <<< "$checks")" security "meta is stored as JSON"

stored="$(api GET "/v1/tenant/custom-checks/$FULL_ID")"
expect_eq "$(jq -r .slug <<< "$stored")" "$FULL_SLUG" "Plerion has the full check"
expect_eq "$(jq -r .defaults.remediation <<< "$stored")" "Nothing to fix. This check comes from a test run." "Plerion stored the remediation"
expect_eq "$(jq -r .defaults.meta.run_id <<< "$stored")" "$PLERION_TEST_RUN_ID" "Plerion stored the metadata"
expect_eq "$(jq '.defaults.meta.threshold == 0.0000005' <<< "$stored")" true "Plerion stored the metadata number by value"
expect_eq "$(jq -r .body <<< "$stored")" "$(jq -r .full.value.body <<< "$checks")" "Plerion stored the policy exactly"

lookups="$(outputs lookups)"
expect_eq "$(jq -r .by_id.value.slug <<< "$lookups")" "$FULL_SLUG" "the data source finds the check by ID"
expect_eq "$(jq -r .by_slug_id.value <<< "$lookups")" "$FULL_ID" "the data source finds the check by slug"
expect_eq "$(jq -r .by_id.value.meta <<< "$lookups")" "$(jq -r .full.value.defaults.meta <<< "$checks")" \
  "the data source returns the same meta as the resource"
expect_eq "$(jq -c .run_role_check_slugs.value <<< "$lookups")" "[\"$MINIMAL_SLUG\"]" \
  "the list data source filters by asset type"

if [[ -n "$PLERION_TEST_INTEGRATION_ID" ]]; then
  dry_run="$(outputs dry-run)"
  expect_eq "$(jq -r .status.value <<< "$dry_run")" SUCCEEDED "the dry run succeeds"
  expect_eq "$(jq -r .custom_check_id.value <<< "$dry_run")" "$FULL_ID" "the dry run previews the saved check"
  expect_eq "$(jq -r .findings_match_total.value <<< "$dry_run")" true "the dry run returns every finding it counts"
fi

step "Plan again with nothing changed"
for unit in "${plan_units[@]}"; do
  expect_no_changes "$unit"
done

step "Update the checks"
export PLERION_TEST_PHASE=update
run_logged update-apply tg_all apply
checks="$(outputs checks)"
FULL_SLUG="$(jq -r .full.value.slug <<< "$checks")"
expect_eq "$(jq -r .full.value.id <<< "$checks")" "$FULL_ID" "a renamed check keeps its ID"
expect_eq "$FULL_SLUG" "$PLERION_TEST_RUN_ID-full-renamed" "the slug is renamed in place"
expect_eq "$(jq -r .full.value.version <<< "$checks")" 2 "the version goes up on update"
expect_eq "$(jq -r .full.value.target.scope <<< "$checks")" all "the scope changes"
expect_eq "$(jq -r .full.value.defaults.informational <<< "$checks")" true "informational changes"
expect_eq "$(jq -r '[.full.value.description, .full.value.additional_assets, .full.value.defaults.meta, .full.value.defaults.remediation] | map(tostring) | join(",")' <<< "$checks")" \
  "null,null,null,null" "removed attributes are null in state"
expect_eq "$(jq -r .minimal.value.version <<< "$checks")" 1 "an unchanged check keeps its version"

stored="$(api GET "/v1/tenant/custom-checks/$FULL_ID")"
expect_eq "$(jq -c '[has("description"), has("additionalAssets"), (.defaults | has("meta")), (.defaults | has("remediation"))]' <<< "$stored")" \
  "[false,false,false,false]" "Plerion removed the attributes too"

lookups="$(outputs lookups)"
expect_eq "$(jq -r .by_id.value.slug <<< "$lookups")" "$FULL_SLUG" "the data source sees the new slug"
expect_eq "$(jq -r .by_id.value.version <<< "$lookups")" 2 "the data source sees the new version"

for unit in "${plan_units[@]}"; do
  expect_no_changes "$unit"
done

step "Repair a change made outside Terraform"
api GET "/v1/tenant/custom-checks/$MINIMAL_ID" |
  jq '{slug, title: "Edited outside Terraform", description, target, additionalAssets, defaults, type, body} | with_entries(select(.value != null))' |
  api PUT "/v1/tenant/custom-checks/$MINIMAL_ID" --data @- > /dev/null
expect_changes checks
run_logged drift-apply tg_all apply
checks="$(outputs checks)"
expect_eq "$(jq -r .minimal.value.title <<< "$checks")" "Terragrunt suite minimal check" "apply restores the title"
expect_eq "$(jq -r .minimal.value.version <<< "$checks")" 3 "the version counts both changes"
expect_no_changes checks

step "Recreate a check deleted outside Terraform"
api DELETE "/v1/tenant/custom-checks/$FULL_ID" > /dev/null
expect_changes checks
run_logged recreate-apply tg_all apply
checks="$(outputs checks)"
NEW_FULL_ID="$(jq -r .full.value.id <<< "$checks")"
expect_ne "$NEW_FULL_ID" "$FULL_ID" "the check is created again with a new ID"
expect_eq "$(jq -r .full.value.version <<< "$checks")" 1 "the new check starts at version 1"
expect_eq "$(jq -r .by_slug_id.value <<< "$(outputs lookups)")" "$NEW_FULL_ID" "the data sources follow the new check"
FULL_ID="$NEW_FULL_ID"
for unit in "${plan_units[@]}"; do
  expect_no_changes "$unit"
done

step "Import a check by its slug"
run_logged state-rm tg_unit checks state rm plerion_custom_check.minimal
expect_changes checks
run_logged import tg_unit checks import plerion_custom_check.minimal "$MINIMAL_SLUG"
imported_id="$(tg_unit checks show -json 2> /dev/null |
  jq -r '.values.root_module.resources[] | select(.address == "plerion_custom_check.minimal") | .values.id')"
expect_eq "$imported_id" "$MINIMAL_ID" "the import finds the existing check"
expect_no_changes checks

step "Refuse a slug that is already taken"
conflict_log="$LOG_DIR/conflict-apply.log"
conflict_code=0
tg_unit conflict apply -auto-approve -no-color > "$conflict_log" 2>&1 || conflict_code=$?
expect_ne "$conflict_code" 0 "applying a duplicate slug fails"
if ! grep -q "A custom check with this slug already exists" "$conflict_log"; then
  show_log "$conflict_log"
  fail "the error does not explain the slug conflict"
fi
pass "the error explains the slug conflict and how to import"
expect_eq "$(run_check_ids | wc -l | tr -d ' ')" 2 "no duplicate check was created"

step "Destroy the checks"
run_logged destroy tg_all destroy
expect_eq "$(run_check_ids | wc -l | tr -d ' ')" 0 "Plerion has no checks left from this run"
expect_eq "$(api_status "/v1/tenant/custom-checks/$MINIMAL_ID")" 404 "the minimal check is gone"
