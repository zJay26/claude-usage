# Dashboard recordings / 界面演示

These recordings use the repository's embedded Dashboard with `scripts/demo-api.js`. All sessions, counters, source paths and exports are synthetic. No local Claude transcripts, credentials or personal usage are read. Capture fails on page errors, unexpected external requests or accounting/error banners.

动图使用实际页面交互，按录制时的原始时序播放，未加速。光标移动约 420–850 ms，点击后至少停顿 650 ms，搜索逐字输入间隔 140 ms，每个章节另留 2 秒阅读时间。GIF 和 MP4 的时长会自动核对；静止画面通过帧去重节省体积，保留原有停顿。

## Files

| File | Content |
|---|---|
| [claude-usage-demo.gif](claude-usage-demo.gif) | 中文实速动图，99.04 秒、1120 px 宽、最高 20 fps |
| [claude-usage-demo-en.gif](claude-usage-demo-en.gif) | English walkthrough, 98.12 seconds at original speed |
| [claude-usage-demo-zh.mp4](claude-usage-demo-zh.mp4) | 中文高清录屏，1440 × 1000 |
| [claude-usage-demo-en.mp4](claude-usage-demo-en.mp4) | English full-resolution recording, 1440 × 1000 |
| [social-preview.png](social-preview.png) | 暖色项目分享封面，1280 × 640 |

## Walkthrough

Overview → a 90-minute query → hourly drilldown → daily calendar → project breakdown → task tree and collapse/expand → Session search → combined Fast/model filters → JSON/CSV choices and an actual synthetic CSV download → Windows/WSL sources → model and cache-lifetime prices → display preferences → dark theme → language switch.

## Reproduce

Requires Node.js, Playwright Chromium, `ffmpeg` and `ffprobe` on `PATH`.

```sh
npm ci
npx playwright install chromium
npm run capture:media
```

Optional `MEDIA_LOCALE=zh-CN` or `MEDIA_LOCALE=en` records only one language. The static server uses a free loopback port and a temporary browser profile. The script shuts them down after capture. It does not install claude-usage or add startup entries.

Chapter screenshots, timing audits and the sample CSV are written to ignored `dist/media-review/<locale>/`. Published assets contain only the synthetic demo. Screenshot-only capture is available through `npm run capture:screenshots` after `npm run build:demo`.
