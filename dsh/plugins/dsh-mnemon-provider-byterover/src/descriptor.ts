import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "byterover",
  "label": "ByteRover",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "local",
  "workspaceBinding": "optional-override",
  "summary": "Local-first hierarchical knowledge tree accessed through the brv CLI.",
  "summaryI18nKey": "overview.providerSummary.byterover",
  "origin": "third-party",
  "capabilities": {
    "search": true,
    "browse": false,
    "graph": false,
    "entities": false,
    "related": false,
    "remember": true,
    "link": false,
    "forget": false,
    "writeMode": "async-extracting",
    "deletionMode": "unsupported"
  },
  "fields": [
    {
      "key": "cliPath",
      "label": "brv executable",
      "scope": "service",
      "input": "path",
      "required": false,
      "defaultValue": "brv",
      "i18nKey": "overview.providerField.cliPath"
    },
    {
      "key": "defaultDirectory",
      "label": "Default knowledge directory",
      "scope": "service",
      "role": "global-location",
      "input": "path",
      "required": false,
      "discoveryDefaultFrom": "workingDirectory",
      "i18nKey": "overview.providerField.defaultDirectory"
    },
    {
      "key": "workingDirectory",
      "label": "Knowledge directory",
      "scope": "memory",
      "input": "path",
      "required": false,
      "i18nKey": "overview.providerField.workingDirectory"
    },
    {
      "key": "apiKey",
      "label": "Cloud API key",
      "scope": "service",
      "input": "secret",
      "required": false,
      "i18nKey": "overview.providerApiKey"
    }
  ]
}
