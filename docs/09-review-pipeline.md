# Review pipeline

## Freeze

1. Zidentyfikuj exact execution i lease.
2. Stop workload; timeout → hard stop → inspect stopped.
3. Zablokuj/revoke dalszy write.
4. Sprawdź workspace typy plików i symlinks.
5. Capture tracked/untracked/deleted/modes.
6. Utwórz commit/tree i retention ref.
7. Zapisz candidate + event atomowo.

## Verify

1. Stwórz fresh verifier z pinned image.
2. Materializuj exact review tree, nie mutable worktree.
3. Uruchom trusted argv commands z timeoutem.
4. Zapisz exit/duration/log hashes/image digest.
5. Destroy verifier; workspace candidate pozostaje immutable.

## Review bundle

Human view musi zawierać:

- task/specification,
- base/review/tree OIDs,
- full diff lub pointer + hash,
- effective policy i hash,
- verification commands/results,
- worker/verifier image digests,
- target ref i current base status,
- warnings/residual risks.

## Approve

Approval jest explicit dla `--candidate <OID>`. Nie ma approval „najnowszego”. Revocation pozostaje w audycie.

## Integrate

Pod lockiem ponownie sprawdź tuple i target base. Dozwolony tylko exact fast-forward. Konflikt/stale kończy się bez zmiany refs.

## Push

Oddzielna komenda i capability. Sprawdza, że local target wskazuje exact integrated commit. Nie force-pushuje w PoC.
