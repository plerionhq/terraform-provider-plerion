resource "plerion_aws_service_account" "this" {
  control_plane_role_arn = aws_iam_role.plerion_control_plane.arn
  instance_profile_arn   = aws_iam_instance_profile.plerion_appliance.arn
  name                   = "workload-scanners"
}
