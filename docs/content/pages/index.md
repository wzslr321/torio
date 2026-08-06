---
output: site/index.html
nav: Home
order: 1
title: Torio — your AI second brain on a Linux VM you control
description: Torio is a thin control plane that runs an AI second brain and your coding projects on a Linux VM on your workstation. It creates the VM, keeps the backend healthy, and leaves credentials, the tunnel, and every Git write to you.
---

<section class="hero">
<p class="hero-eyebrow">Thin control plane · macOS and Linux</p>
<h1 class="hero-title">Your AI second brain, on a Linux VM you actually control.</h1>
<p class="hero-lede">Torio creates the Linux VM, runs a Hermes backend on the VM's own loopback, tells you in one command whether the whole stack is healthy, and gives the model access to exactly the repositories you listed. You open the connection, you hold the credentials, you decide what gets committed.</p>
<p class="hero-actions"><a class="btn btn-primary" href="tutorials.html#get-started">Get started</a><a class="btn btn-quiet" href="#pieces">See how it fits together</a></p>
</section>

## The pieces, and who owns them {#pieces}

Torio is not the AI, not the VM, and not the chat window. It is the layer that
brings those three into a known-good state and then gets out of the way. Six
moving parts, split across the two machines:

<div class="stack" aria-label="How the pieces fit together">
<section class="stack-zone">
<p class="stack-where">On your host</p>
<div class="stack-items">
<div class="stack-item"><b>torio</b><span>The Torio CLI, and the only part of this that we wrote. It creates the VM, installs the backend as a service, proves the service is genuinely answering, and registers your projects. It has no daemon and holds no state of its own.</span></div>
<div class="stack-item"><b>Hermes Desktop</b><span>The chat app you already use, pointed at a URL instead of a local backend. This is where you actually talk to the second brain and to the code.</span></div>
<div class="stack-item"><b>Your <code>ssh -L</code></b><span>The one and only route from the host to the backend. You open it in a terminal you can see, and close it when you are done.</span></div>
</div>
</section>
<p class="stack-pipe"><span>localhost:19119 → the VM's 127.0.0.1:9119</span></p>
<section class="stack-zone">
<p class="stack-where">Inside the Linux VM</p>
<div class="stack-items">
<div class="stack-item"><b>Hermes backend</b><span>A <code>hermes serve</code> process, run as a user systemd service so it survives logout and restarts on its own. It binds <code>127.0.0.1</code> <em>of the guest</em>: nothing on your network, and nothing on your host, can reach it except through your tunnel.</span></div>
<div class="stack-item"><b>Your Second Brain</b><span>A private Markdown vault, versioned by its own Git repository and registered with Hermes so any session can search it. Torio can import an existing vault into it; there is no export, because getting data back out is a copy you run yourself.</span></div>
<div class="stack-item"><b>Your projects</b><span>Repository clones on the VM's own Linux filesystem — not on a host share reaching back into your home directory. The model sees the ones you registered, and no others.</span></div>
</div>
</section>
</div>

The VM is a Lima instance. Torio creates it from a pinned template, installs
pinned versions of what the backend needs, and can reconcile it later without
touching your data — the same run twice changes nothing the second time.

## What a session looks like {#session}

First run is a short, ordered sequence. After that, `torio serve status` is the
one to remember: it reports the systemd unit, the loopback endpoint and the
Hermes version, so a backend that stopped answering names itself instead of
leaving you to guess.

<div class="terminal" aria-label="Example session">
<div class="terminal-bar"><span class="dot"></span><span class="dot"></span><span class="dot"></span><span class="terminal-title">first run</span></div>
<pre class="terminal-body"><code><span class="tp">$</span> torio vm init          <span class="tok-comment"># create the VM from a pinned template</span>
<span class="tp">$</span> torio vm bootstrap     <span class="tok-comment"># install and verify what the backend needs</span>
<span class="tp">$</span> torio serve install
<span class="tp">$</span> torio serve start      <span class="tok-comment"># backend up on the VM's own loopback</span>
<span class="tp">$</span> torio serve status     <span class="tok-comment"># and prove it answers</span>
<span class="tok-ok">Backend ready on http://127.0.0.1:9119/api/status</span>
  systemd:  active (active=true, enabled=true)
  endpoint: 200 (ready=true)
<span class="tp">$</span> torio brain init       <span class="tok-comment"># your private, searchable Markdown vault</span>
<span class="tp">$</span> torio project add my-service https://github.com/you/my-service --use
<span class="term-note">— then, in a second terminal you leave open —</span>
<span class="tp">$</span> ssh -L 19119:127.0.0.1:9119 …  <span class="tok-comment"># your tunnel; now Desktop can connect</span></code></pre>
</div>

From there you point Hermes Desktop at `http://127.0.0.1:19119`, paste the
session token the backend requires, and work. On the code side the loop is
yours end to end: edit or ask for edits, run a check that reads rather than
writes, read `git diff` — and when you decide something should leave the VM,
`torio project shell` gives you a session that can push and takes the capability
back when you exit.

## Projects are a list you keep {#projects}

The model can only see repositories you have registered. That list is a plain
file you own: each entry names a repository and where its clone lives on the
guest, and it holds no credentials of any kind. Adding a project clones it into
the VM and registers it with Hermes; nothing is discovered, scanned, or picked
up automatically because it happened to be on disk.

Read access to a private repository is your job, set up by you on the guest.
Torio never stores a token, never prompts for one, and never passes one to the
model — a workspace it prepared has no push credentials in it at all.

## What Torio will not do {#limits}

The narrowness is the point. Torio never
holds or prompts for a credential — the one it forwards is your SSH agent, into
a session you opened, for as long as you keep it open. It never opens a tunnel
or exposes a port beyond the guest's loopback. It never commits, pushes, merges,
or tags — those are yours, always. It never deletes or re-images a VM, and it
takes no data back out of one. And it is not an agent platform: no task queue,
no dispatcher, no fleet of autonomous workers running while you sleep.

## The rest of these docs {#map}

<ul class="modes">
<li><a class="mode-card" href="tutorials.html"><span class="name">Tutorials</span><span class="what">The guided end-to-end run, complete on one page. Start here.</span></a></li>
<li><a class="mode-card" href="how-to.html"><span class="name">How-to guides</span><span class="what">One task at a time, once you are set up: the tunnel, the session token, Desktop, providers, attaching repositories, pushing, your own editor, and troubleshooting.</span></a></li>
<li><a class="mode-card" href="reference.html"><span class="name">Reference</span><span class="what">Every <code>torio</code> command, exit codes, and the fixed boundaries.</span></a></li>
<li><a class="mode-card" href="explanation.html"><span class="name">Explanation</span><span class="what">Why Torio is narrow, and why credentials and Git writes stay human-only.</span></a></li>
</ul>

> **Not deployed yet.** This is the documentation source, prepared for
> **Vercel**. No Vercel project and no `torio.dev` domain are connected or
> configured here — see [Deployment](reference.html#deployment).
