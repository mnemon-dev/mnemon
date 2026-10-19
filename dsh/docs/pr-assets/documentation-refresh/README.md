# Documentation refresh verification / 文档整理验证

Current documentation media: [capture provenance](../../assets/showcase/README.md).

## Initial verification

Production code, loader patch, lockfile and package versions are unchanged from main `d94dfec`. The new executable work is limited to the documentation guard and its tests. Verification used Node `24.19.0` and pnpm `10.13.1` on macOS.

| Check | Result |
|---|---|
| `MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm verify` | Passed: 122 test files / 1,058 tests, including 8 documentation regressions and the real Native create/write/recall/forget case; 1 Windows-only file/test skipped |
| Deterministic build | 39 generated files matched |
| Headless | 38 tools / 7 representative Mnemon tools; legacy entry spelling and complete Starter disabling verified |
| Package verification | 46 Starter files; 11 public runtime imports / 26 public type dependencies; strict publint and attw passed |
| `pnpm verify:plugins` | 16 independent plugin repositories and 17 packed artifacts passed installation, public-consumer checks, typecheck, tests and builds; real DSH activated the packed Starter and all three optional enhancements |
| `pnpm release:check` / `pnpm release:intent` | Existing exact composition valid; changeset covers all 17 packages whose published README/metadata changed; no publication or version bump performed |
| Documentation | Local file/anchor links, English/Chinese counterpart paths and the full official plugin catalog checked; external network availability is not a CI prerequisite |
| README/media | Both languages rendered through GitHub's Markdown API and inspected at desktop/390px widths; overview and Runtime image URLs returned HTTP 200; 1440×960 H.264 video loaded and played for 22.72 seconds |
| Real desktop WebUI | Runtime and Document creation; Native space creation/activation/synchronization/content; three enhancement switches persisted after reopening Settings and returned to Off in both languages; no page-level JS exceptions |
| Narrow product WebUI | **Failed visual inspection**, described below; not conflated with the narrow README rendering check |

The first verification was stopped by the existing absolute-README-image gate, which caught the interim relative media links. The links now use an immutable media revision. An overlapping standalone package check also ran during a rebuild and saw missing generated declarations; final verification was rerun serially and passed. Upstream packages emit missing-sourcemap warnings; no runtime fix or suppression is included here.

No paid-model evaluation, cloud-Provider account test, physical-device test or new release-upgrade scenario was run. The Native binary used was the installed 0.2.7 executable identified in the media provenance, not a newly downloaded checksum-verified release. Historical evidence and benchmark scores are retained, not replaced by these checks.

本次只改变文档与文档检查，不改变生产代码、配置、数据格式或版本。完整验证共 1,058 项通过，真实 Native 用例已运行，仅跳过 Windows 专用测试；16 个独立插件仓库和 17 个产物验证通过。中英文桌面与 README 展示检查通过，产品 390px 窄屏失败单独保留。未执行付费模型、真实云账号或物理手机测试，也未发布新版本。

## Follow-up: Chinese copy, centered headers and links

The 2026-09-06 follow-up restores the original centered title, language selector, seven badges, introduction and navigation. GitHub release/version, stars and both dshfind badges are retained. The Stars badge points to the repository homepage because the public stargazers page returned 404. No Git tag is created, deleted or moved: the 43 local tags and 43 remote tags (58 ref lines including peeled annotated tags) match the pre-change snapshots exactly.

Chinese entry-point copy, the overview, ownership/data-flow/sequence labels and workflow explanations are localized; package names, API identifiers and executable configuration stay unchanged. Previous heading anchors remain available. The two overview SVG/PNG pairs now live in language-specific directories; historical media and tagged release links remain intact. Rendering also caught three English sequence-message semicolons interpreted as Mermaid statement separators; commas fix that syntax without changing the sequence.

The self-contained documentation revision is `8466e3560a3b9de4e9f4b7302cbf005c84e8e69f`: all 769 local links and 35 Markdown anchors passed before the npm-facing READMEs were changed to absolute permalinks into that revision. Internal navigation in the snapshot stays relative, so changing language or returning to its homepage does not fall back to an older README. This fixes the 20 new-directory/media URLs that returned 404 on the not-yet-merged `main`.

| Follow-up check | Result |
|---|---|
| Full `pnpm verify` with the existing Native CLI | 122 files / **1,063 tests passed**; Windows-only 1 file/test skipped; deterministic build, Headless, public exports, package contents, publint and attw passed |
| Documentation regressions | **13 passed**; 3 newly reproduced failures cover HTML headings, URL entities/autolinks and query parameters; both headers guard the seven badges and localized overview |
| `pnpm verify:plugins` | **16 independent plugin repositories / 17 packed artifacts passed**, including real DSH packed-Starter and three-enhancement activation |
| Release metadata | All 17 package versions unchanged; release check and changeset coverage passed; no publication |
| README rendering | GitHub Markdown API + github-markdown-css 5.8.1 in Chrome 152; both languages at 1280px and 390px, centered headers, 9 loaded images per page, no horizontal page overflow or page-level JS exceptions |
| Diagrams | Both SVG overviews rendered at 2×; all 6 architecture Mermaid diagrams parsed and rendered with Mermaid 11.12.0 |
| External URLs | 143 unique HTTP(S) targets across 164 Markdown files: **141 returned 200**, with the two qualified cases below |

- The upstream `dsh-external/issues#603` is in a **private** repository. Its authenticated GitHub API lookup succeeded; unauthenticated HTTP returned 404. The roadmap retains the link and states the access requirement.
- The npm package page returned a **403 anti-bot challenge**, including in Chrome. `npm view dsh-mnemon name version dist-tags --json` confirmed the public package and `latest: 0.5.2`. This verifies the package identity, not successful page rendering; no challenge was bypassed.

Network checks are a dated manual audit, not a promise of future availability or a network dependency for local CI. README-only permalink changes were followed by the documentation and package checks. The initial browser assertion was also corrected to accept Chromium's `-webkit-center` spelling for HTML paragraph alignment; it was a harness mismatch, not a layout defect. No model calls or new product UI changes are part of this follow-up.

| README preview | Desktop | Narrow |
|---|---|---|
| English | [1280px](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/documentation-refresh/readme-en-1280.png) | [390px](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/documentation-refresh/readme-en-390.png) |
| 简体中文 | [1280px](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/documentation-refresh/readme-zh-CN-1280.png) | [390px](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/documentation-refresh/readme-zh-CN-390.png) |

本轮恢复中英文标题区居中与原有 7 枚徽章，保留全部 Git tags；补齐中文图示与流程说明，并保留旧锚点。完整验证更新为 1,063 项通过，独立插件及产物再次通过。143 个外链中 141 个直接返回 200，私有 Issue 经登录 API 核实，npm 防机器人限制单列，不冒充页面访问通过。上方四张截图是 **README 渲染证据**，不是新一次产品 WebUI 测试；下方既有产品窄屏失败仍原样保留。

## Narrow-screen finding

The unmodified production implementation at `d94dfecddc0976b9d0620d1e0e387a5340bd6526`, running on published DSH `0.1.2-rc.1`, showed clipping after resizing an open Memory System settings dialog from 1440×960 to 390×844. The dialog container remained inside the viewport, but the controls and workbench stacking were not visually usable. This is **not a passing responsive-layout result**. It was found during documentation capture; this documentation PR does not change runtime or DSH UI code.

[Original 390px resize screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/documentation-refresh/narrow-resize-390.png)

A fresh 390×844 touch-enabled browser context was checked separately: open the Sidebar, then Settings → Memory System. The resize-specific overlapping workbench was absent, but the fixed-width settings navigation left the content column approximately 106px wide, producing excessively wrapped text. [Fresh narrow-context screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/documentation-refresh/narrow-fresh-390.png). This is browser emulation, not a physical mobile-device test, and is also not accepted as a usable layout.

真实当前实现缩到 390px 后，虽然弹窗外框仍在视口内，设置内容和工作台层叠却出现裁切。因此不能仅凭 bounding-box 断言就宣布窄屏通过。本次保留原始失败截图，不修改业务界面，也不将其作为主展示素材。桌面中英文操作结果与这一失败分别记录。

另用新的 390px 触控浏览器上下文复核：没有同样的工作台层叠，但固定导航让内容列只剩约 106px，正文过度换行；仍不作为可用的窄屏布局通过。
