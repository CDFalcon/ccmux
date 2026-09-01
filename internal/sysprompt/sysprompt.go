// Package sysprompt holds shared fragments of the agent system prompt that
// launcher scripts embed. Fragments are spliced into double-quoted bash
// strings inside Sprintf format strings, so they must not contain double
// quotes, backticks, dollar signs, backslashes, or percent signs.
package sysprompt

// SharePaneDoc documents the `ccmux pane` commands agents can use to share
// live program output with the user in a split pane below their own.
const SharePaneDoc = `You can share live output with the user in a tmux split pane below your main pane:
    ccmux pane open [command...]   open the shared pane; runs the command there, or an interactive shell if omitted
    ccmux pane run <command...>    run a shell command in the shared pane (opens a shell pane first if needed)
    ccmux pane close               close the shared pane
Use it to show the user long-running programs and outputs: dev servers, log tails, test watchers, builds, or demos. Output stays visible to the user even after the command exits.`
