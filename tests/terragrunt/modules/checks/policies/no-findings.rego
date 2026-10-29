package custom

# Never reports an asset. Plerion runs every new or changed check against
# the tenant's integrations, and a test check must not leave findings behind.
default result := []
