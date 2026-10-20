# 验证记录

日期：2026-10-20；主机：Windows amd64。

## 已通过

- 插件 `go test -race -v ./...`：7 个顶层测试及 9 个门限子场景全部通过。
- 插件 `go vet ./...`：通过。
- Windows amd64 原生 DLL：构建通过，并由官方 CPA v8.0.35 实际加载、注册。
- Linux amd64 原生 SO：交叉构建通过，检查 ELF 动态依赖和 `cliproxy_plugin_init` 导出符号通过。
- 前端新增百分比测试：3/3 通过；`bun run lint`、`bun run build` 通过。
- 浏览器实际操作：右侧“限制”按钮、中文弹窗、读取已保存的 5h/7d 门限、修改保存、保存后立即刷新文件启停开关通过。
- 原生插件与真实 CPA 宿主联调：低额度禁用、被禁用文件继续查询、额度恢复后启用、查询失败保持现状、等于门限保持现状、关闭页面后继续检查、进程重启后保留规则并继续自动控制、关闭限制后保持手动启停通过。
- 文件变更只更新 `disabled`，测试中原有令牌与其他字段保留；额度查询后重新读取凭证再写入，避免覆盖查询期间已刷新的令牌。

原生联调使用临时 CPA 实例和本地模拟额度服务，间隔设为 1 秒以验证后台周期；正式产物默认 60 秒。测试专用 DLL 通过构建参数把额度地址指向本地模拟服务。安装包中的正式产物使用正常 ChatGPT 额度地址，不含此测试地址，不含测试账号或管理密钥。

## 上游已有失败

`bun run verify` 的测试阶段：**1633 通过，5 失败**。在未修改的上游提交 `fe2963330f32a933f562282165f62ae52cf57145` 单独复测：**1630 通过，5 失败**，失败项目完全一致：

1. logs controller lifecycle wiring / connection invalidation source contract。
2. provider probe proxy forwarding / sponsor form source contract。
3. quota toolbar presentation contracts / clear action source contract。
4. XaiQuotaBody unavailable weekly usage / subscription and reset rendering。
5. XaiQuotaBody unavailable weekly usage / monthly amount rendering。

最后两项在本机显示 `US$` 而断言期待 `$`。没有修改这些无关功能。完整 verify 因这些已有失败未通过，lint 和生产构建已分别运行通过。

浏览器中原版页面请求未配置的 OAuth 别名和禁用模型项时有两个 404；新增插件接口 GET/PUT 均返回 200。

## 验证边界

- 未使用真实订阅凭证访问上游，不宣称已验证真实账号额度返回。
- 未在 Linux/Docker 内实际加载运行；Linux 目前验证到构建、架构和动态库导出。
- 未部署到用户现有 CPA。安装包需要按 README_CN.md 放入目标服务。
- 临时 CPA、模拟额度服务与测试浏览器均已关闭；临时上游基线工作树已移除。
