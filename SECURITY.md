# Security Policy

## Supported versions

lazyssm is pre-1.0 and ships from `main`. Security fixes land on the latest
release and the `main` branch.

| Version            | Supported |
|--------------------|-----------|
| Latest release     | ✅        |
| `main`             | ✅        |
| Older tagged builds| ❌        |

## Reporting a vulnerability

**Do not open a public issue for security problems.**

Report privately through GitHub:

1. Go to the repository's **Security** tab → **Advisories** → **Report a vulnerability**
   (direct link: https://github.com/chalvinwz/lazyssm/security/advisories/new).
2. Describe the issue, affected version (`lazyssm --version`), and steps to reproduce.

This routes the report privately to the maintainer via GitHub Private
Vulnerability Reporting — no public disclosure until a fix is ready.

## What to expect

lazyssm is maintained by a single person on a best-effort basis. You can expect
an initial acknowledgement within a few days. Once a fix is ready it will be
released as a new tag, and the advisory will be published crediting the reporter
(unless you prefer to remain anonymous).

## Scope

lazyssm brokers AWS SSM Session Manager sessions using your local AWS
credentials. Reports about credential handling, command/argument injection into
the AWS CLI / SSM plugin, or accidental secret exposure (logs, pins file) are in
scope. Vulnerabilities in AWS itself should go to
[AWS Security](https://aws.amazon.com/security/vulnerability-reporting/).
