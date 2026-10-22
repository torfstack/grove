# 0007: Download validation across metadata-version changes

Date: 2026-10-22. Status: accepted; implemented with offline/race and full Linux live verification.

## Context

Linux and macOS live initial-sync runs have rejected a changed Drive metadata
version despite matching size/MD5 and structure. A focused Linux retry passed.
The exact cause of the version advance and equality of unreported revision/time
fields have not been established. The user selected this reliability work and
requested a spec and dedicated branch before implementation.

## Agreed decision

Use the stronger content-revision and structural comparison in
[spec 000008](../spec/000008-download-metadata-version-design.md) to tolerate
metadata-version increases during download verification and download recovery.
Require explicit matching revision/time evidence for the exception; retain
strict equality when that evidence is absent. Preserve planned journal
snapshots and reconcile newer version metadata through normal later scans.

Sleeping or retrying can mask the failure. Revision-specific downloads introduce
unneeded API and retention changes. The proposal instead keeps the current
download contract and strengthens evidence at its existing boundaries.

## Consequences and limits

The rule must cover initial download recovery and replacement recovery, not
only the post-transfer check. It must not loosen move, upload, or Trash
preconditions, infer deletion, or weaken artifact ownership and checksum checks.
No schema migration or new remote mutation is expected. The full live suite
must pass; a successful focused retry is insufficient acceptance evidence.

The user approved spec 000008 and native execution of its paired plan. The
implementation passed local checks and the full Linux live suite in 279.377
seconds. Independent branch review found no actionable findings; integration
remains.
