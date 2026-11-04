# 轻传盒 · clipbox

一个给小内存 VPS 用的极简文件 / 文字传输站。无登录、无数据库、单二进制、开箱即用。

- 64MB 内存也能跑：上传大文件时进程 RSS 约 6MB
- 网页根目录直接上传 / 下载，新文件自动替换旧文件
- 多个文字窗口，在线编辑、自动保存、一键复制
- 服务器上 `curl` 一条命令直传，不装客户端
- systemd 开机自启，一条命令安装
- 数据就是普通文件，随时备份、随时迁移

> 无鉴权裸奔，仅适合自己临时传东西。请使用高位端口，不要放敏感数据到公网。

## 一键安装

```sh
curl -fsSL https://raw.githubusercontent.com/spverk/clipbox/main/install.sh | sudo sh -s -- --port 28080
```

安装脚本会：下载对应架构的二进制、创建 `clipbox` 系统用户、写入 systemd 服务、设置内存上限、开机自启、尽力放行防火墙端口。

可选参数：

```sh
--port 28080            # 监听端口（默认 28080）
--dir /var/lib/clipbox   # 数据目录
--bind 0.0.0.0           # 监听地址
--max-size 2G            # 单文件上限
--repo owner/repo        # 自定义 GitHub 仓库
--uninstall              # 卸载（保留数据）
```

卸载：

```sh
curl -fsSL https://raw.githubusercontent.com/spverk/clipbox/main/install.sh | sudo sh -s -- --uninstall
```

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
- systemd 单元设置 `GOMEMLIMIT=40MiB`、`GOGC=50`、`MemoryHigh=48M`、`MemoryMax=56M`。

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
