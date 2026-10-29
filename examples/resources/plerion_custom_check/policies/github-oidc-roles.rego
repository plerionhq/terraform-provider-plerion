package custom

# Reports every role whose trust policy lets GitHub Actions assume it without
# naming a repository. Roles that pass are left out of the result.
default result := []

result := [{"id": role.id, "status": "FAILED"} |
	role := input.assets[_]
	statement := role.data.AssumeRolePolicyDocument.Statement[_]
	statement.Effect == "Allow"
	contains(statement.Principal.Federated, "token.actions.githubusercontent.com")
	not statement.Condition.StringLike["token.actions.githubusercontent.com:sub"]
]
