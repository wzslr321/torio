# Deployment handoff — the Torio documentation site (Vercel)

This directory is the static Torio documentation site: plain HTML, one CSS
file, and one small `docs.js` that adds the colour-theme toggle and the copy
buttons on code blocks. There is no framework, bundler, or CDN, and both
JavaScript features are progressive enhancements — with scripts blocked the
pages still read and navigate normally. It is **Vercel-ready source only**. Nothing in this
repository connects a Vercel account, creates a project, deploys, or configures a
domain — those are human-only, post-merge steps.

The `.html` files here are **generated** from `docs/content/` by
`scripts/build_docs.py` and committed, so Vercel needs **no build command**: it
serves this directory as-is. Contributors run `make docs` after editing a source;
`make validate` fails if a committed page has drifted. Do not edit the `.html`
files directly.

Repository-side configuration is limited to a minimal
[`../vercel.json`](../vercel.json) that declares the static output directory
(`site`). No framework, Node/npm, build command, or server is introduced.

## Human post-merge flow (high level)

1. **Connect the repository.** In Vercel, import/connect this private GitHub
   repository as a new project.
2. **Configure the static output directory.** Ensure the project serves this
   `site/` directory as static output (the `vercel.json` above declares it; set
   the Root/Output Directory in project settings if prompted). No build command
   is required.
3. **Verify the deployment.** Let Vercel produce a preview deployment, open it,
   and confirm all five pages render and all navigation works, then promote to
   production.
4. **Add the domain.** Add `torio.dev` to the Vercel project and configure its
   DNS in Vercel and/or the domain provider, following their current
   verified instructions.

This handoff intentionally contains **no** tokens, credential-setup mechanics,
secret values, or specific DNS records. Account, project, deployment, and
`torio.dev`/DNS configuration are performed by a human outside this repository.
