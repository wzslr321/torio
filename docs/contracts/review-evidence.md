# Review, verification and approval evidence

## Sequence

```text
STOP worker
→ revoke write
→ capture tracked + untracked + deleted files
→ create review commit/tree
→ run trusted commands in fresh verifier sandbox
→ hash/store evidence
→ human review
→ approve exact tuple
→ check target HEAD == base
→ fast-forward integrate
→ optional explicit push
```

## Artifact tuple

Approval wiąże co najmniej:

```text
project_id
hb_task_id
execution_id
base_commit
review_commit
review_tree
review_diff_sha256
effective_policy_sha256
verification_record_sha256
worker_image_digest
verifier_image_digest
target_ref
```

Zmiana któregokolwiek elementu unieważnia approval.

## Candidate capture

- Worker nie tworzy trusted review commit.
- Control plane capture uwzględnia tracked, untracked, deletions i file modes.
- Symlinks są walidowane; target escaping workspace jest odrzucany.
- Special files, sockets, devices i nested Git metadata są odrzucane.
- Review ref jest retention pointerem; OID jest identity.

## Verification

- Verifier jest świeży dla każdego candidate.
- Candidate snapshot jest jedynym source input.
- Network i credentials wynikają z effective policy; Demo B ma `none`/zero.
- Commands są argv arrays z trusted registry.
- Raw logs trafiają do artifact store z redakcją i ograniczeniem rozmiaru.
- Evidence zawiera hashes/pointers, exit codes, durations i image digest.
- Worker-reported tests są informacyjne i nigdy nie zastępują verification.

## Integration

PoC dopuszcza tylko:

```text
current target OID == approved base_commit
AND candidate/approval/evidence still valid
THEN atomic fast-forward target → review_commit
```

Jeśli target się przesunął, `STALE_BASE`. Nie wykonuj automatycznego cherry-picka ani rebase po approval.
