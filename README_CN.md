# CPA 账号额度限制

在认证文件右侧启停开关旁增加“限制”按钮，分别设置该文件的 5h、7d 最低剩余百分比。

开启后，后台默认每 60 秒检查一次。任一有效窗口低于门限就禁用文件，全部有效窗口高于门限就启用；等于门限或查询失败时保持现状。留空的门限和账号实际不存在的窗口不参与判断。关闭限制后保持文件当前状态，恢复手动管理。

页面关闭后检查仍然运行；被禁用的文件也会查询，额度恢复后自动启用。保存设置会立即检查一次。规则按认证文件名保存在服务端，重命名文件后需要重新设置。

## 安装

适用基线：官方 CLIProxyAPI **v8.0.35**，Linux amd64，支持原生插件的 glibc/Debian Docker 镜像。不要使用名称包含 `no-plugin` 的服务端版本。尚未在 Linux 容器中运行验证；已在 Windows 官方 v8.0.35 实例完成原生插件联调。

1. 将安装包中的 `limit-gpt.so` 放到 CPA 的 `plugins/` 目录。
2. 备份现有 `static/management.html`，用安装包的 `management.html` 替换。按钮需要这份管理前端。
3. 在现有 CPA 配置中合并以下内容，保留已有其他配置及插件：

```yaml
management:
  disable-auto-update-panel: true

plugins:
  enabled: true
  dir: "plugins"
  configs:
    limit-gpt:
      enabled: true
      state_file: "plugins/limit-gpt.json"
      interval_seconds: 60
      # 如 CPA 访问上游需要代理，在这里填相同代理。
      # 认证文件自身的 proxy_url 优先；留空时使用环境代理。
      proxy_url: ""
```

4. 重启 CPA，刷新管理页面，在认证文件右侧点击“限制”，填写并保存。

`state_file` 相对于 CPA 工作目录；Docker 中把 `plugins/` 挂载为持久目录。关闭面板自动更新是为了保留新增按钮。

现有 Docker 部署可添加挂载（按实际宿主路径调整）：

```yaml
volumes:
  - ./plugins:/CLIProxyAPI/plugins
  - ./management.html:/CLIProxyAPI/static/management.html:ro
```

恢复原版时，先关闭各文件的自动限制，按需手动启用文件，再移除插件、还原原管理页面。

## 构建

Linux/Docker 构建插件和单文件管理前端：

```sh
docker build --output type=local,dest=dist/linux-amd64 .
```

Windows 本地构建需 Go 1.26+、GCC、Bun 1.3.14：

```powershell
./scripts/build.ps1
```

输出位于 `dist/windows-amd64/`。无需改变 CPA 的账号调度方式。

## 验证

插件测试：在 `plugin/` 运行 `go test -race ./...` 和 `go vet ./...`。

前端检查：在 `frontend/` 运行 `bun run verify`。本次新增测试通过；上游原版与修改版均有相同的 5 项已有测试失败，详见 `VERIFICATION.md`。

## 源码

- `plugin/`：Go C ABI 原生插件、规则保存、后台检查、额度解析和文件启停。
- `frontend/`：官方管理前端，新增小按钮和弹窗；基于提交 `fe2963330f32a933f562282165f62ae52cf57145`。
- `Dockerfile`：Linux 构建；`scripts/build.ps1`：Windows 构建。

CPA 上游接口基于提交 `ae3ee3be303fb07fb8173279923413005bf32177`（v8.0.35）。本插件仓库为 https://github.com/Oldcucumber/Limit-gpt ，使用 GPL-3.0-only 许可证。第三方代码保留原始许可证与署名，详见 `THIRD_PARTY.md`。

额度检查只读取上游额度，不发模型生成请求。不保证精确停在保留比例，接受检查间隔内的额外消耗。
