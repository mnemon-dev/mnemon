# Issue Triage

[简体中文](./ISSUE_TRIAGE.zh-CN.md) | **English**

This document defines the dsh-mnemon Issue label system, classification, information requests, and closure process. The goal is to keep every open Issue searchable, verifiable, assignable, and traceable.

## Labels

| Label | Meaning | When to use it |
| --- | --- | --- |
| `bug` | A report of unexpected behavior, an error, regression, or compatibility problem | The reporter selects Bug report; maintainers verify the symptom and reproduction during triage |
| `enhancement` | A new capability or improvement to an existing capability | The request has a clear use case, constraints, and acceptance result |
| `documentation` | Missing, obsolete, or inconsistent README, docs, or comments | No runtime behavior changes |
| `question` | A usage or design question | The Issue asks how something works or why it behaves that way |
| `good first issue` | Suitable for a new contributor | Small scope, clear acceptance criteria, and no deep security or storage context required |
| `help wanted` | Approved for community implementation | Maintainers accept the approach and have no internal schedule |
| `duplicate` | Duplicates an existing Issue | The symptom, root cause, or objective substantially overlaps another Issue |
| `invalid` | Not a repository problem, a public security report, or persistently missing information | The Issue cannot proceed through the template |
| `wontfix` | Deliberately not planned | Out of scope, conflicts with a security boundary, or has insufficient benefit |

Labels use GitHub's default names. When adding a long-lived label, update the Issue Forms, automation, and this document. Do not create one-off temporary labels.

## Classification process

Triage new Issues in this order:

1. **Security check**: if the Issue contains a vulnerability, token, credential, private memory, or sensitive log, remove the public material and direct the reporter to `SECURITY.md`.
2. **Information check**: look for the symptom, expected outcome, reproduction steps and environment, plus classification and duplicate-search confirmation. Ask for specific missing details without closing the Issue. Evidence may be plain text, JSON or other code blocks anywhere in the report; separate evidence, smoke tests, code references and patch proposals are optional.
3. **Duplicate check**: search open and closed Issues and merged PRs. Label duplicates with `duplicate`, link the original Issue in a comment, and close the duplicate.
4. **Assign a type**: apply `bug`, `enhancement`, `documentation`, or `question` based on the objective. The information check adds `bug` for the Bug report type, including CLI/API submissions; this is classification, not confirmation that a defect has been verified.
5. **Verify current state**: reproduce against current `main` and the latest release. If another PR or release already solved the problem, cite its commit, PR, or version and close the Issue.
6. **Confirm scope**: route problems that require DSH core or an upstream Provider change to the upstream project. Keep only a concrete compatibility-layer or workaround task here.
7. **Open for contribution**: add `help wanted` when the scope is clear and external implementation is accepted; add `good first issue` when it is also suitable for a new contributor.

## Closure criteria

Close an Issue when any of these conditions apply, and always leave a traceable explanation:

- **Implemented**: the change is on `main`; cite the PR or commit and add the minimum fixed release after publication;
- **Superseded**: a more complete or safer implementation exists; cite the superseding PR;
- **Duplicate**: link the original Issue;
- **Answered**: provide the conclusion and an authoritative documentation link;
- **Obsolete**: the dependency, interface, or design has changed;
- **Out of scope**: an upstream DSH, Mnemon, or Provider change is required; identify the upstream destination;
- **Information missing long-term**: after a maintainer requests specific details and allows time to respond, the report still cannot be investigated; explain the unresolved gap and apply `invalid`.

Use GitHub's `completed` or `not_planned` closure reason. Do not close without an explanation. Reporters may request reopening after adding new evidence.

Formatting, the location of a log, a missing code reference or the absence of a patch alone are not closure reasons. A reopened Issue stays available for maintainer review; the template check never closes it.

## Pull Request linkage

- Fix PRs should use `Fixes #<n>` or `Closes #<n>` to link an accepted Issue;
- reviews must compare the PR with current `main` and determine whether history or a more complete solution has superseded it;
- proving that an old version had a problem does not by itself establish that a current PR should merge;
- an Issue may close when the fix reaches `main`; add the minimum fixed version after release.

## Maintainer command reference

```sh
gh issue edit <n> -R mnemon-dev/mnemon --add-label "bug,good first issue"
gh issue comment <n> -R mnemon-dev/mnemon --body "Explanation"
gh api -X PATCH repos/omdsh-dev/dsh-mnemon/issues/<n> \
  -f state=closed -f state_reason=completed

gh issue list -R mnemon-dev/mnemon --state open \
  --json number,title,labels --jq '.[] | select(.labels|length==0)'
```

## Automation

- `.github/workflows/issue-template-enforcer.yml` reads current state on opening, reopening and editing, adds `bug` for Bug reports, and maintains one bilingual reminder for missing basic information. The reminder updates after corrections; this check never changes Issue state;
- `.github/workflows/issue-dedup.yml` posts a bilingual explanation, links the original Issue, labels a highly similar open Issue as a possible duplicate, and closes it;
- `.github/workflows/pr-contribution-rules.yml` validates PR titles, the bilingual template, AI disclosure, local verification, compatibility information, and user-visible evidence;
- `.github/workflows/reject-docs-pr.yml` posts a bilingual explanation and closes documentation-only PRs from external contributors; repository collaborators with `write`, `maintain`, or `admin` permission are exempt.

Automation performs initial screening only. Reporters may explain differences and request reopening; maintainers make the final decision.
