resource "plerion_aws_service_account_region" "sydney" {
  account_id         = plerion_aws_service_account.this.account_id
  region             = "ap-southeast-2"
  vpc_id             = aws_vpc.plerion_appliance.id
  subnet_id          = aws_subnet.plerion_appliance.id
  security_group_id  = aws_security_group.plerion_appliance.id
  queue_url          = aws_sqs_queue.plerion_appliance_scan.url
  max_instance_limit = 10
}
