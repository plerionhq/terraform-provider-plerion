package custom

# Reports only buckets carrying a tag value no real bucket has, so it has the
# shape of a real policy without leaving findings behind.
result := [{"id": bucket.id, "status": "FAILED"} |
	bucket := input.assets[_]
	bucket.data.Tags["plerion-terragrunt-suite"] == "never-set"
]
