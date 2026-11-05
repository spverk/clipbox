# 轻传盒 · clipbox

一个给小内存 VPS 用的极简文件 / 文字传输站。无登录、无数据库、单二进制、开箱即用。

- 64MB 内存也能跑：上传大文件时进程 RSS 约 6MB
- 网页根目录直接上传 / 下载，新文件自动替换旧文件
- 多个文字窗口，在线编辑、自动保存、一键复制
- 服务器上 `curl` 一条命令直传，不装客户端
- systemd / OpenRC 开机自启；纯容器环境自动后台运行并提供 `clipboxctl`
- 数据就是普通文件，随时备份、随时迁移

> 无鉴权裸奔，仅适合自己临时传东西。请使用高位端口，不要放敏感数据到公网。

## 一键安装

有 sudo 的普通用户：

```sh
curl -fsSL https://raw.githubusercontent.com/spverk/clipbox/main/install.sh | sudo sh -s -- --port 28080
```

已经是 root（提示符为 `#`）或系统里没有 `sudo` 时，去掉 `sudo`：

```sh
curl -fsSL https://raw.githubusercontent.com/spverk/clipbox/main/install.sh | sh -s -- --port 28080
```

> 不要把 URL 包在反引号里，也不要写成 `... | sudo` 之类的形式，否则 shell 会先执行 URL 而不是下载脚本。

安装脚本会：下载对应架构的二进制、自动识别 systemd / OpenRC / 纯容器环境、配置开机自启（容器内则后台启动并提供 `clipboxctl`）、设置内存上限、尽力放行防火墙端口。

可选参数：

```sh
--port 28080            # 监听端口（默认 28080）
--dir /var/lib/clipbox   # 数据目录
--bind 0.0.0.0           # 监听地址
--max-size 2G            # 单文件上限
--chunk-threshold 90M    # 超过该大小改用分片上传（适配 Cloudflare 100MB 单请求上限）
--chunk-size 8M          # 每片大小
--repo owner/repo        # 自定义 GitHub 仓库
--uninstall              # 卸载（保留数据）
```

卸载（root 用户去掉 `sudo`）：

```sh
curl -fsSL https://raw.githubusercontent.com/spverk/clipbox/main/install.sh | sudo sh -s -- --uninstall
```

## 没有 systemd 时（Podman / Docker / LXC）

安装脚本会自动识别环境：

- 有 systemd：写入服务并开机自启
- 有 OpenRC（Alpine 等）：写入 `/etc/init.d/clipbox` 并加入默认运行级别
- 都没有：直接后台启动，并生成 `/usr/local/bin/clipboxctl`

容器内没有 init，容器重启后需要重新拉起：

```sh
clipboxctl start     # 启动
clipboxctl status    # 状态
clipboxctl stop      # 停止
clipboxctl restart   # 重启
```

如果是 Podman / Docker，把 `clipboxctl start` 写进容器的启动命令或 entrypoint；宿主机 `--restart=always` 只负责重启容器，不会自动拉起容器里的进程。

完全手动安装（不跑安装脚本）时：

```sh
curl -fL -o /usr/local/bin/clipbox \
  https://github.com/spverk/clipbox/releases/latest/download/clipbox-linux-amd64
chmod +x /usr/local/bin/clipbox
mkdir -p /var/lib/clipbox

GOMEMLIMIT=16MiB GOGC=50 /usr/local/bin/clipbox \
  --bind 0.0.0.0 --port 28080 --dir /var/lib/clipbox --max-size 2G
```

ARM 机器把下载地址里的 `amd64` 改成 `arm64`。

## 网页使用

打开 `http://你的IP:28080`：

- 文件槽：拖拽、点击选择或直接粘贴文件，上传后显示文件名、大小、时间，可下载 / 替换 / 删除。
- 文字窗口：默认 3 个窗口，可新建到 30 个；输入后 0.7 秒自动保存；切换窗口前会先保存；“复制全文”一键复制。

## 命令行直传

```sh
# 上传文件，保留原文件名
curl -T backup.tar.gz http://IP:28080/api/upload

# 上传并指定文件名
curl -T backup.tar.gz "http://IP:28080/api/upload?name=backup.tar.gz"

# 下载当前文件
curl -fsSL http://IP:28080/api/download -o backup.bin

# 写入第 1 个文字窗口
printf '要传的文字' | curl --data-binary @- \
  -H 'Content-Type: text/plain' -X PUT http://IP:28080/api/texts/1

# 读取第 1 个文字窗口
curl -fsSL http://IP:28080/api/texts/1

# 健康检查
curl -fsSL http://IP:28080/api/health
```

上传也支持 `POST /api/upload`（`multipart/form-data`，字段名 `file`），浏览器用的就是它。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| GET | `/api/state` | 文件信息、窗口列表、上限、版本 |
| POST / PUT | `/api/upload` | 上传文件（multipart 或 raw body） |
| POST | `/api/upload/init` | 初始化分片上传，body 为 `{"name":"...","size":123}` |
| PUT | `/api/upload/chunk?id=&offset=` | 按顺序上传分片（raw body） |
| POST | `/api/upload/complete?id=` | 校验分片完整后原子替换为当前文件 |
| DELETE | `/api/upload?id=` | 放弃分片上传并清理临时文件 |
| GET | `/api/download` | 下载当前文件，支持 Range |
| DELETE | `/api/file` | 删除当前文件 |
| GET | `/api/texts` | 窗口列表 |
| POST | `/api/texts` | 新建窗口，body 可为 `{"title":"..."}` |
| GET | `/api/texts/{id}` | 读取窗口内容 |
| PUT / PATCH | `/api/texts/{id}` | 写入内容，JSON 或纯文本 |
| DELETE | `/api/texts/{id}` | 删除窗口（至少保留一个） |

## 数据目录

```
/var/lib/clipbox/
├── file/
│   ├── current.bin     # 当前文件，上传时原子替换
│   └── meta.json       # 文件名、大小、时间
└── texts/
    ├── index.json      # 窗口索引
    ├── 1.txt
    ├── 2.txt
    └── ...
```

没有数据库，备份直接打包整个目录即可。

## 内存设计

- 上传全程流式：`io.Copy` 写临时文件，完成后 `os.Rename` 原子替换，不把文件读进内存。
- multipart 用 `MultipartReader` 逐段读取，避免整文件缓冲。
- 下载用 `http.ServeContent` 流式输出。
- 单文件默认上限 2G，文字单窗口上限 2MiB，最多 30 个窗口。
- systemd 单元设置 `GOMEMLIMIT=16MiB`、`GOGC=50`、`MemoryHigh=20M`、`MemoryMax=28M`，给同一台机器上的 sing-box 等其它服务留出内存。
- 64M 机器与 sing-box 同机时，建议 sing-box 也设 `GOMEMLIMIT=16MiB` 并关闭 WARP；整机预算约为系统 8–12MB + sing-box 20–30MB + clipbox 5–8MB。

实测同时上传 200MB 文件时，进程 RSS 约 6MB。

## 手动编译

```sh
go build -o clipbox .
./clipbox --bind 0.0.0.0 --port 28080 --dir ./data --max-size 2G
```

交叉编译并生成校验和：

```sh
./build.sh
```

## 安全说明

- 没有账号、密码、Token，任何能访问端口的人都能上传 / 下载 / 改文字。
- 只用高位端口并不等于安全，建议只监听内网地址，或在前面套一层带访问控制的反向代理。
- 数据目录权限为 `0750`，服务以独立的 `clipbox` 用户运行。

## License

MIT
