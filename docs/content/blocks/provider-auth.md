## Configure a model provider {#provider-auth}

Until a provider is configured, starting a session fails at agent init — for
example `agent init failed: No credentials stored`. Check what the guest
currently has:

```bash
torio vm ssh -- sudo -u claude -- claude --version
```

`torio vm ssh` forwards no stdin or TTY by design, so the interactive picker cannot
be driven through the control plane. Use an interactive shell on the guest
instead. `limactl shell` logs you in as the Lima user, so become the agent
identity explicitly:

```bash
limactl shell torio-claude-code     # interactive shell in the VM (Lima user)
sudo -iu claude                     # become the agent identity
claude                              # sign in through the agent's own flow
```

Which provider and credential to use is the operator's judgement. The secret
never enters the repository, its evidence, or any pull request or comment.
