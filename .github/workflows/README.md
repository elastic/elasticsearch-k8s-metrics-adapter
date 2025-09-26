# Workflows

## auto-approve-renovate-prs

This workflow automatically approves Renovate PRs created by the Renovate bot, but only if:

- The PR is created by `elastic-renovate-prod[bot]`
- The PR has the `renovate-auto-approve` label (which is added via the `labels` configuration in our Renovate configuration)
- The workflow is triggered by the `auto_merge_enabled` event on the `main` branch

### Approval Window

Approval is restricted to: Monday -> Thursday

This workflow is designed to work alongside Renovate's "platform native" PR-based automerge functionality, which uses GitHub's native automerge functionality to merge the PR once CI checks pass. This two-step process enables a safer and more controlled dependency updates, without requiring Renovate bypass branch protection requirements.

### Why use `auto_merge_enabled` as the event trigger?

The workflow uses the `auto_merge_enabled` event instead of the standard `opened` or `synchronize` pull request events to ensure that PR approval only occurs when auto-merge has been explicitly enabled for a pull request. This provides an extra layer of control and safety by:

- Preventing automatic approval of all Renovate PRs as soon as they are opened.
- Ensuring that only PRs which had auto-merge enabled (either manually or by Renovate configuration) are auto-approved.
- Supporting a two-step automerge process, where PRs are first created by Renovate and only then approved by GitHub Actions Bot for merging during the defined approval window.

This approach helps teams maintain oversight of dependency updates while still benefiting from automation.