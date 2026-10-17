# Help and shell completion

## Help

Every level of the CLI explains itself, on stdout, and exits 0:

```sh
tnl help                  # every command, one line each
tnl help route            # the group's commands
tnl help route add        # one command
tnl route add --help      # the same; -h works too
```

A command's help gives its usage, what each argument and flag means and its default, the environment variable behind a flag where there is one, which hosts it runs on (the Edge host, an Outpost or both) and an example or two. Flags show as `--flag`; the single-dash form keeps working.

A wrong command or a missing argument prints a short usage line and a pointer to the matching `tnl help ...` on stderr. Exit codes don't change: 2 for an unknown command, 1 for a refused one, 78 for a bad setting of `tnl edge` or `tnl connect`, and `tnl status` has its own.

In the Outpost build, the commands that need the full build are listed as "full build only" in the help.

The help is written in one table in the source. A test checks it against the commands, against every flag they define and against the CLI table in the [spec](../spec/v1.md#7-cli-reference), so the three can't drift.

## Shell completion

`tnl completion bash|zsh|fish` prints a script. It completes commands, subcommands and flags. Where an Edge or an Outpost runs on the same host and its socket can be opened by you, it also completes Outpost names, Route Hostnames and Zones, through the local socket. It waits up to two seconds, and completes nothing from the process when it can't answer.

`install.sh` does not install completion: it writes into shell directories that differ by distribution and user, and a script on the Outpost host seldom matters. Add one line to your shell's startup file instead.

### bash

Add to `~/.bashrc`:

```sh
source <(tnl completion bash)
```

Or install it for every user: `tnl completion bash | sudo tee /etc/bash_completion.d/tnl`.

### zsh

Add to `~/.zshrc`, after `compinit`:

```sh
source <(tnl completion zsh)
```

### fish

```sh
tnl completion fish > ~/.config/fish/completions/tnl.fish
```

Fish completes commands, flags and names, but not file paths.

### Names need the right user

`tnl outpost rm <TAB>` asks the running Edge, which only its socket's owner (usually root) can open. A shell that can't open the socket completes the command and flags and no names. Set `TNL_STATE_DIR` to pick the process when there are several on the host, as for any command.
