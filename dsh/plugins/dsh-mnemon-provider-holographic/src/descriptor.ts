import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "holographic",
  "label": "Holographic",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "local",
  "workspaceBinding": "optional-override",
  "summary": "Local structured fact memory with trust scoring, entity resolution, and compositional retrieval.",
  "summaryI18nKey": "overview.providerSummary.holographic",
  "origin": "third-party",
  "capabilities": {
    "search": true,
    "browse": true,
    "graph": true,
    "entities": true,
    "related": true,
    "remember": true,
    "link": false,
    "forget": true,
    "writeMode": "exact",
    "deletionMode": "hard"
  },
  "fields": [
    {
      "key": "dataPath",
      "label": "Fact store path",
      "scope": "service",
      "role": "global-location",
      "input": "path",
      "required": false,
      "i18nKey": "overview.providerField.dataPath"
    },
    {
      "key": "defaultTrust",
      "label": "Default trust",
      "scope": "memory",
      "input": "number",
      "required": true,
      "defaultValue": 0.5,
      "min": 0,
      "max": 1,
      "i18nKey": "overview.providerField.defaultTrust"
    },
    {
      "key": "minTrust",
      "label": "Minimum recall trust",
      "scope": "memory",
      "input": "number",
      "required": true,
      "defaultValue": 0.3,
      "min": 0,
      "max": 1,
      "i18nKey": "overview.providerField.minTrust"
    }
  ]
}
