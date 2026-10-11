# 文档中心

**简体中文** | [English](../en/README.md) | [项目首页](../../README.zh-CN.md)

dsh-mnemon 为 DeepSeek Harness 提供三类记忆：每一轮都会用到的**运行时记忆**、问题需要时才检索的**项目档案**，以及存放在你所选 Provider 上的长期证据**记忆空间**。主策略决定它们如何参与每一轮对话，主策略与增强都在插件页中选择。从默认的分层策略开始即可，日常使用无需管理插件。

[![可组合记忆插件页中的记忆组合面板](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/zh-CN/plugin-composition.jpg)](./guides/ui-guide.md#在插件页中)

## 使用记忆系统

| 要完成的事 | 指南 |
|---|---|
| 从零安装并启动：网页、桌面版或命令行，到第一条记忆 | [安装与启动](./guides/installation.md) |
| 保存并用上第一批记忆 | [快速开始](./guides/getting-started.md) |
| 熟悉对话、记忆系统与插件页 | [界面指南](./guides/ui-guide.md) |
| 了解每个组件与策略能做什么 | [能力地图](./guides/capabilities.md) |
| 选择并接入长期记忆后端 | [Provider 指南](./guides/memory-providers.md) |
| 备份、迁移存储或排查故障 | [运维指南](./guides/operations.md) |
| 升级已有安装 | [兼容性与升级](./reference/compatibility.md) |

## 查阅具体约定

| 问题 | 参考 |
|---|---|
| 有哪些设置，在哪里编辑、保存在哪里？ | [配置参考](./reference/configuration.md) |
| 数据保存在哪，怎样共享和归档？ | [存储模型](./reference/storage-model.md) |
| 读取、写入和整理何时发生？ | [生命周期与流程](./reference/workflows.md) |
| 提供哪些工具、命令、RPC 通道与界面区域？ | [接口参考](./reference/interfaces.md) |

## 制作扩展

| 要完成的事 | 开发文档 |
|---|---|
| 理解 Source、Strategy、View、归属与界面区域 | [架构设计](./development/architecture.md) |
| 开发 Source、Strategy、增强或 Provider | [插件开发](./development/extensions.md) |
| 让皮肤适配 Mnemon 背景与透明度 | [皮肤开发](./development/skin-integration.md) |
| 构建、测试与真实 WebUI 截图 | [开发与验证](./development/README.md) |
| 为独立包管理版本和发布 | [发布流程](./development/releasing.md) |

## 最新变化

[v0.5.24](./releases/v0.5.24.md) 让实体页统计并列出带有某个实体的全部记忆，选中后立即显示，相关记忆只在需要时查找。[v0.5.23](./releases/v0.5.23.md) 减少了 Runtime 压缩、Document 查询、空间发现、多路召回、批量准备与 Holographic 排名中的重复工作，保留结果顺序和存储格式。[全部版本](./releases/README.md) · [路线图](./roadmap.md) · [历史验收证据](../pr-assets/README.md)

指南描述当前版本。截图与录屏来自 [v0.5.19 图集](../assets/webui-v0.5.19/README.md)，安装步骤来自[安装图集](../assets/install-v0.5.19/README.md)；带日期的 PR 记录只证明其标注的代码修订与环境。内部 Host RPC 不属于对外插件 SDK。
