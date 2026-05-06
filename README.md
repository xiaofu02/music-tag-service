# Music Tag Service

基于 Go 语言开发的音乐标签管理服务，支持自动刮削、多线程处理和 Web UI 操作。

本项目是 [music-tag-web](https://github.com/xhongc/music-tag-web) 的 Go 语言轻量化复刻版本，专注于高效、稳定、易用的音乐标签管理。

## 功能特性

- 🎵 **多源搜索**：支持网易云音乐、QQ 音乐等多个平台
- 🏷️ **自动 tagging**：自动读取和写入音乐文件的 ID3/FLAC 标签
- 📁 **自动导入**：监控文件夹，新文件自动刮削入库
- ✏️ **批量重命名**：支持自定义模板批量重命名文件
- 📂 **文件整理**：按艺术家/专辑自动整理音乐文件
- 🔄 **跳过已处理**：智能跳过已刮削文件，避免重复处理
- 🌐 **Web UI**：简洁的中文 Web 界面
- 🔌 **RESTful API**：方便 Agent 调用或二次开发
- 📝 **歌词嵌入**：自动获取并嵌入歌词
- 🖼️ **封面嵌入**：自动下载并嵌入专辑封面

## 快速开始

### 下载运行

1. 从 [Releases]([https://github.com/your-repo/releases](https://github.com/xiaofu02/music-tag-service/releases) 下载最新版本
2. 运行程序：

```bash
./music-tag-service.exe -music-dir "D:\Music"
```

或双击运行（默认使用用户音乐文件夹）。

### Web 界面

打开浏览器访问：`http://localhost:8080`

### 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-port` | HTTP 服务端口 | 8080 |
| `-music-dir` | 音乐文件夹路径 | 用户 Music 目录 |
| `-token` | API 认证令牌 | 空（不启用） |

## 项目结构

```
music-tag-service/
├── main.go              # 程序入口
├── config.json          # 配置文件（自动生成）
├── ffmpeg/              # FFmpeg 目录（需要自行放置）
│   ├── ffmpeg.exe
│   └── ffprobe.exe
├── web/                 # Web 前端
│   ├── index.html
│   ├── app.js
│   └── style.css
└── internal/            # 内部包
    ├── api/             # HTTP API 和业务逻辑
    ├── config/          # 配置管理
    ├── scraper/         # 音乐搜索爬虫
    └── tag/             # 标签读写
```

## API 接口

### 健康检查

```
GET /api/v1/health
```

### 获取文件夹列表

```
GET /api/v1/folder
```

### 搜索音乐

```
GET /api/v1/search?title=歌曲名&artist=艺术家
```

### 获取标签信息

```
GET /api/v1/tag?path=文件路径
```

### 写入标签

```
POST /api/v1/tag
Content-Type: application/json

{
  "path": "D:\\Music\\song.mp3",
  "title": "歌曲名",
  "artist": "艺术家",
  "album": "专辑名",
  "year": 2024,
  "cover_url": "http://...",
  "lyrics": "歌词内容"
}
```

### 批量重命名

```
POST /api/v1/batch-rename
Content-Type: application/json

{
  "paths": ["D:\\Music\\song1.mp3", "D:\\Music\\song2.mp3"],
  "template": "{artist} - {title}"
}
```

### 整理文件

```
POST /api/v1/organize
Content-Type: application/json

{
  "paths": ["D:\\Music\\song.mp3"],
  "target_dir": "D:\\Music\\Organized",
  "structure": "{artist}/{album}/{title}",
  "dry_run": false
}
```

### 自动导入控制

```
POST /api/v1/auto-import/start
POST /api/v1/auto-import/stop
```

## 配置文件

程序运行后会在同级目录生成 `config.json`：

```json
{
  "auto_import": {
    "enabled": false,
    "watch_path": "C:\\Users\\YourName\\Music",
    "concurrency": 4,
    "auto_tag": true,
    "providers": ["netease", "qmusic"],
    "mode": "hard",
    "overwrite": false
  },
  "default_settings": {
    "concurrency": 4,
    "providers": ["netease", "qmusic"],
    "mode": "hard",
    "overwrite": false,
    "save_cover": false,
    "save_lyrics": false
  },
  "watch_folders": []
}
```

| 配置项 | 说明 |
|--------|------|
| `enabled` | 是否启用自动导入 |
| `watch_path` | 监控文件夹路径 |
| `concurrency` | 并发刮削数量 |
| `auto_tag` | 是否自动刮削 |
| `providers` | 搜索源列表 |
| `mode` | 匹配模式：`hard`（严格）或 `simple`（宽松） |
| `overwrite` | 是否覆盖已有标签 |

## FFmpeg 支持

FLAC 等无损格式的标签写入需要 FFmpeg。

### 安装 FFmpeg

1. 下载 [FFmpeg Windows 版本](https://www.gyan.dev/ffmpeg/builds/)
2. 将 `ffmpeg.exe` 和 `ffprobe.exe` 放入程序目录的 `ffmpeg/` 文件夹

## 批量重命名模板

支持的变量：

| 变量 | 说明 |
|------|------|
| `{title}` | 歌曲标题 |
| `{artist}` | 艺术家 |
| `{album}` | 专辑名 |
| `{year}` | 年份 |
| `{track}` | 音轨号 |
| `{ext}` | 文件扩展名 |

示例：

- `{artist} - {title}` → `周杰伦 - 晴天.mp3`
- `{album}/{track} - {title}` → `七里香/01 - 晴天.mp3`

## 自动导入说明

启用自动导入后，程序会：

1. 监控指定文件夹的文件变化
2. 检测到新文件时，自动读取文件名进行搜索
3. 匹配成功后自动写入标签
4. 已处理的文件会记录在 `processed_files.json`，避免重复处理
5. 文件重命名/移动后自动更新记录

## 开发构建

```bash
# 克隆项目
git clone https://github.com/your-repo/music-tag-service.git

# 进入目录
cd music-tag-service

# 安装依赖
go mod tidy

# 构建
go build -o music-tag-service.exe .
```

## 致谢

本项目是 [music-tag-web](https://github.com/xhongc/music-tag-web) 的 Go 语言轻量化复刻版本，参考了其设计理念，专注于高效、稳定、易用的音乐标签管理。

## 免责声明

**禁止任何形式的商业用途**，包括但不仅限于售卖/打赏/获利，不得使用本代码进行任何形式的牟利/贩卖/传播，再次强调仅供个人私下研究学习技术使用，不提供下载音乐本体！

本项目仅以纯粹的技术目的去学习研究，如有侵犯到任何人的合法权利，请致信 874247667@qq.com，我将在第一时间修改删除相关代码，谢谢！

## 许可证

本项目基于 **GPL V3.0** 许可证发行，以下协议是对于 GPL V3.0 的补充，如有冲突，以以下协议为准。

**词语约定**：本协议中的"本项目"指 music-tag-service 项目；"使用者"指签署本协议的使用者；"官方音乐平台"指对本项目内置的包括酷我、网易云、QQ 音乐、咪咕、酷狗音乐、酷我音乐等音乐源的官方平台统称；"版权数据"指包括但不限于图像、音频、名字、歌词等在内的他人拥有所属版权的数据。

**数据来源说明**：本项目的数据来源原理是从各官方音乐平台的公开服务器中拉取数据，经过对数据简单地筛选与合并后进行展示，因此本项目不对数据的准确性负责。使用本项目的过程中可能会产生版权数据，对于这些版权数据，本项目不拥有它们的所有权，为了避免造成侵权，使用者务必在 24 小时内清除使用本项目的过程中所产生的版权数据。

**名称约定**：本项目内的官方音乐平台别名为本项目内对官方音乐平台的一个称呼，不包含恶意，如果官方音乐平台觉得不妥，可联系本项目更改或移除。

**资源来源**：本项目内使用的部分包括但不限于字体、图片等资源来源于互联网，如果出现侵权可联系本项目移除。

**责任声明**：由于使用本项目产生的包括由于本协议或由于使用或无法使用本项目而引起的任何性质的任何直接、间接、特殊、偶然或结果性损害（包括但不限于因商誉损失、停工、计算机故障或故障引起的损害赔偿，或任何及所有其他商业损害或损失）由使用者负责。

**完全免费**：本项目完全免费，仅供个人私下小范围研究交流学习技术使用，且开源发布于 GitHub 面向全世界人用作对技术的学习交流，本项目不对项目内的技术可能存在违反当地法律法规的行为作保证，禁止在违反当地法律法规的情况下使用本项目，对于使用者在明知或不知当地法律法规不允许的情况下使用本项目所造成的任何违法违规行为由使用者承担，本项目不承担由此造成的任何直接、间接、特殊、偶然或结果性责任。

**协议接受**：若你使用了本项目，将代表你接受以上协议。
