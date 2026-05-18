# Mnemon Harness Modules

This directory contains canonical, host-agnostic loop modules.

```text
harness/modules/
├── memory-loop/
├── skill-loop/
└── eval-loop/
```

Each module follows the Loop Module Standard and declares its assets in
`module.json`. Host-specific projection logic belongs under `harness/hosts/`.
