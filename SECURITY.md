# Security policy

Relay runs Claude Code with tool access on a self-hosted runner, driven by
GitHub Issues and a Telegram bot, so a bug in routing, authorization or input
handling can mean code execution on someone's Mac. Reports are welcome.

## Reporting a vulnerability

Please do not open a public Issue. Use GitHub's private reporting instead:
**Security → Report a vulnerability** on
[masteine/relay](https://github.com/masteine/relay/security/advisories/new).

Include what you found, how to reproduce it and what an attacker gains. You
should get an answer within a week.

## Supported versions

Relay is at v0; only the latest release and `main` get fixes.

## Scope

In scope: the `relay` binary (stage wrapper, bot, setup), the workflow
template and the Claude Code plugin in this repository.

Out of scope: running Relay on a public repository (unsupported, see the
README), and the behaviour of Claude Code, GitHub Actions or Telegram
themselves.
