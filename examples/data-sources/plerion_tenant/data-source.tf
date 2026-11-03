data "plerion_tenant" "this" {}

output "external_id" {
  value = data.plerion_tenant.this.external_id
}
