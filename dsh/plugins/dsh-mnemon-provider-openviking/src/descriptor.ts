import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "openviking",
  "label": "OpenViking",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "remote",
  "workspaceBinding": "provider-global",
  "summary": "Filesystem-shaped shared memory with verified exact writes and semantic retrieval.",
  "summaryI18nKey": "overview.providerSummary.openviking",
  "origin": "third-party",
  "capabilities": {
    "search": true,
    "browse": true,
    "graph": false,
    "entities": false,
    "related": false,
    "remember": true,
    "link": false,
    "forget": true,
    "writeMode": "exact",
    "deletionMode": "hard"
  },
  "fields": [
    {
      "key": "endpoint",
      "label": "Endpoint",
      "scope": "service",
      "input": "url",
      "required": true,
      "defaultValue": "http://127.0.0.1:1933",
      "placeholder": "http://127.0.0.1:1933",
      "i18nKey": "overview.providerEndpoint"
    },
    {
      "key": "targetUri",
      "label": "Memory URI",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "viking://user/memories",
      "placeholder": "viking://user/memories",
      "pattern": "^viking://user(?:/(?!\\.{1,2}/)[a-zA-Z0-9_.@-]+)?/memories$",
      "normalize": "trim-trailing-slash",
      "validationMessage": "OpenViking memory URI must be a viking://user/.../memories root",
      "i18nKey": "overview.providerTargetUri"
    },
    {
      "key": "apiKey",
      "label": "API key",
      "scope": "service",
      "input": "secret",
      "required": false,
      "i18nKey": "overview.providerApiKey"
    },
    {
      "key": "account",
      "label": "Account",
      "scope": "service",
      "input": "text",
      "required": false,
      "i18nKey": "overview.providerAccount"
    },
    {
      "key": "discoveryUser",
      "label": "User key owner (skip admin)",
      "scope": "service",
      "input": "text",
      "required": false,
      "maxLength": 200,
      "pattern": "^(?!\\.{1,2}$)(?!.*@.*@)[a-zA-Z0-9_.@\\-]+$",
      "validationMessage": "OpenViking discovery user must be a safe user ID",
      "i18nKey": "overview.providerDiscoveryUser"
    },
    {
      "key": "user",
      "label": "User",
      "scope": "memory",
      "input": "text",
      "required": false,
      "i18nKey": "overview.providerUser"
    },
    {
      "key": "actorPeerId",
      "label": "Agent peer",
      "scope": "memory",
      "input": "text",
      "required": false,
      "defaultValue": "dsh",
      "i18nKey": "overview.providerActorPeer"
    }
  ]
}
