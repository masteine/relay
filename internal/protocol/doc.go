// Package protocol is the Issue protocol of Relay: the contract between the
// bot, the GitHub Actions workflow and the Claude Code plugin.
//
// Everything that lives in a GitHub Issue is defined here and nowhere else:
// labels and their transitions, stage comments with their machine-readable
// markers, slash commands, the reconcile rule that turns the Issue state into
// the next action, the routing of webhook events, and branch and spec names.
//
// The package has no I/O. Callers fetch the Issue, call into this package and
// apply what comes back. docs/protocol.md is the human-readable version of the
// same rules and the two must change together.
package protocol
