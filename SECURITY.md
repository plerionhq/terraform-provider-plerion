# Security policy

## Reporting a vulnerability

Report security issues privately. Do not open a public issue or pull request.

- Use **Report a vulnerability** on the repository's [Security tab](https://github.com/plerionhq/terraform-provider-plerion/security/advisories/new), or
- email [security@plerion.com](mailto:security@plerion.com).

Include the provider version, the steps to reproduce the issue, and what an attacker could do with it.

## Supported versions

Security fixes are released in a new version of the provider. Upgrade to the latest release to get them.

## Verifying a release

Every release is signed with this GPG key:

```text
CBFF B9D9 41E0 0BF3 8823  8B3C E077 5A57 571A 18D2
```

Terraform checks the signature when it installs the provider. To check a release by hand, download its `SHA256SUMS` and `SHA256SUMS.sig` files and run `gpg --verify` on them.
