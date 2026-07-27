## The everyday loop {#everyday-loop}

Once the VM and backend are up, the day-to-day loop is the same whichever editor
or interface you use:

1. Open the checkout as `hermes`, or drive it from a Desktop session.
2. Edit, or let your AI tool edit, files in the checkout.
3. Run the one documented, non-destructive check.
4. Review the change: `git -C /home/hermes/projects/REDACTED-PROJECT diff` and `… status`.
5. Decide manually whether to commit or push.

Commit and push are **human-only** and out of scope for Torio: it automates no
part of step 5.
