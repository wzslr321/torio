---
output: site/index.html
nav: Home
order: 1
title: "Torio: agent context inside a VM"
description: Torio runs an agent backend in a Linux VM with named repositories and a private Markdown vault. Host credentials stay outside the VM, MCP OAuth belongs to a separate guest identity, and Torio's Git write path stays operator-controlled.
---

<section class="hero">
<p class="hero-eyebrow">Thin control plane · macOS and Linux</p>
<h1 class="hero-title">Agents need context.<br>Credentials need a boundary.</h1>
<p class="hero-lede">Torio creates a Linux VM, runs Hermes or Claude Code inside it, attaches the repositories you name, and keeps a private Markdown vault the agent can search. Host Git and provider credentials stay outside the VM. Private SSH reads use a guest-generated deploy key, MCP OAuth belongs to a separate guest identity, and operator Git write capability exists only inside a session you open.</p>
<p class="hero-actions"><a class="btn btn-primary" href="tutorials.html#get-started">Get started</a><a class="btn btn-quiet" href="#pieces">See how it fits together</a></p>
</section>

## The pieces, and who owns them {#pieces}

Torio is not the AI, not the VM, and not the chat window. It is the layer that
brings those three into a known-good state and then gets out of the way. One VM
runs one agent identity: `--backend` names the agent and Torio finds its box, so
a second backend means a second VM rather than two identities contending over
the same checkouts.

Inside the box the agent runs without permission prompts, and that inversion is
the point. A prompt is a control inside the agent's own process: it can be
ignored, and in practice it is clicked through. The box replaces it with
controls the agent cannot reach: an unprivileged identity with no `sudo`, a
binary it cannot rewrite, no operator write credential, and the edge of a VM.
Here is the default Hermes stack:

<div class="stack" aria-label="How the pieces fit together">
<section class="stack-zone">
<p class="stack-where">On your host</p>
<div class="stack-items">
<div class="stack-item"><b>torio</b><span>The host CLI creates and verifies the VM, installs the backend, and records non-secret instance and project configuration.</span></div>
<div class="stack-item"><b>Hermes Desktop</b><span>The chat app, pointed at a URL instead of a local backend.</span></div>
<div class="stack-item"><b>Your <code>ssh -L</code></b><span>The one and only route from the host to the backend. You open it in a terminal you can see, and close it when you are done.</span></div>
</div>
</section>
<p class="stack-pipe"><span>localhost:19119 → the VM's 127.0.0.1:9119</span></p>
<section class="stack-zone">
<p class="stack-where">Inside the Linux VM</p>
<div class="stack-items">
<div class="stack-item"><b>Hermes backend</b><span>A <code>hermes serve</code> process, run as a user systemd service so it survives logout and restarts on its own. It binds <code>127.0.0.1</code> <em>of the guest</em>: nothing on your network, and nothing on your host, can reach it except through your tunnel.</span></div>
<div class="stack-item"><b>Your Second Brain</b><span>A private Markdown vault, versioned by its own Git repository and registered with Hermes so any session can search it. Torio can import an existing vault into it; there is no export, because getting data back out is a copy you run yourself. The vault's format is a written standard, and it also ships as the <a href="https://github.com/wzslr321/torio/tree/main/brainkit">Brain Kit plugin</a> for Claude Code, usable against a directory of notes with no VM under it at all.</span></div>
<div class="stack-item"><b>Your projects</b><span>Repository clones on the VM's own Linux filesystem — not on a host share reaching back into your home directory. The model sees the ones you registered, and no others.</span></div>
</div>
</section>
</div>

The VM is a Lima instance. Torio creates it from a pinned template, installs
pinned versions of what the backend needs, and can reconcile it later without
touching your data — the same run twice changes nothing the second time.

## What a session looks like {#session}

Run `torio` on a terminal. The hub verifies the box, shows the remaining setup
steps, and runs the corresponding commands. The tutorial lists the same commands
for scripts and manual use.

For Hermes, open your SSH tunnel and point Desktop at
`http://127.0.0.1:19119`. Work in the guest checkout, review `git diff`, then use
`torio project shell` when you decide to push. The forwarded capability ends
with the session.

Once you run more than one box, `torio status` is the command to remember. It
polls every box Torio owns and reports one row each: whether it is running,
what its agent has going, whether anything there is waiting on you, and when it
last provably did work. It exits `0` whatever it finds, so
[your status bar or prompt can call it on a timer](how-to.html#watch-several-agents)
and only the box that wants you is loud.

## Projects are a list you keep {#projects}

The shared project registry stores ids, display names, and remotes. Workspace
paths are derived from the backend and project id, never stored. Adding a
project clones or adopts the exact remote; nothing is discovered from disk.

For a private SSH remote, Torio creates a guest-held deploy key and prints its
public half. You add it to that repository with write access off. Torio cannot
verify the forge setting, so adding it to an account would grant broader access.

## What Torio will not do {#limits}

Torio does not store credentials on the host. An operator shell may forward
your SSH agent; [pin one key](how-to.html#operator-key) to mediate signatures
and optionally grant an agent session the same approval path. MCP OAuth is
stored under the separate broker uid after an explicit login. Torio opens no
public port, never pushes, merges, tags, or releases, and never deletes or
re-images a VM. It is not a task queue, dispatcher, or worker platform.

## The rest of these docs {#map}

<ul class="modes">
<li><a class="mode-card" href="tutorials.html"><span class="name">Tutorials</span><span class="what">The guided end-to-end run, complete on one page. Start here.</span></a></li>
<li><a class="mode-card" href="how-to.html"><span class="name">How-to guides</span><span class="what">One task at a time, once you are set up: the tunnel, the session token, Desktop, providers, attaching repositories, pushing, your own editor, and troubleshooting.</span></a></li>
<li><a class="mode-card" href="reference.html"><span class="name">Reference</span><span class="what">Every <code>torio</code> command, exit codes, and the fixed boundaries.</span></a></li>
<li><a class="mode-card" href="explanation.html"><span class="name">Explanation</span><span class="what">Why Torio is narrow, and how credential custody and Git writes are bounded.</span></a></li>
</ul>
