#!/usr/bin/env python3
"""P4 前端改造脚本：把 iframe 嵌入改为 video + hls.js/flv.js 直接播放。"""

HTML = '../frontend/dist/index.html'

with open(HTML, 'r', encoding='utf-8') as f:
    s = f.read()

orig_len = len(s)


def rep(old, new):
    global s
    if old not in s:
        raise SystemExit(f'找不到替换目标:\n---\n{old[:200]}\n---')
    s = s.replace(old, new, 1)


# ========== 1. HTML 标签改造 ==========
rep('<iframe class="live-iframe" id="frame1"></iframe>',
    '<video class="live-video" id="liveVideo1" autoplay muted playsinline></video>')
rep('<iframe class="live-iframe" id="frame2"></iframe>',
    '<video class="live-video" id="liveVideo2" autoplay muted playsinline></video>')
rep('<iframe class="crop-editor-iframe" id="cropEditorFrame" title="裁切预览"></iframe>',
    '<video class="crop-editor-video" id="cropEditorVideo" autoplay muted playsinline></video>')
rep('</script>\n</body>\n</html>',
    '</script>\n<script src="hls.min.js"></script>\n<script src="flv.min.js"></script>\n</body>\n</html>')


# ========== 2. CSS 改造 ==========
rep('''.live-iframe {
  width: 100%;
  height: 100%;
  border: none;
  background: #000;
}''', '''.live-video {
  width: 100%;
  height: 100%;
  border: none;
  background: #000;
  object-fit: contain;
}''')

rep('''.live-crop-zoom .live-iframe {
  position: absolute;
  left: 0;
  top: 0;
  width: 100%;
  height: 100%;
}''', '''.live-crop-zoom .live-video {
  position: absolute;
  left: 0;
  top: 0;
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
}
.live-crop-zoom .live-local-video {
  position: absolute;
  left: 0;
  top: 0;
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
}''')

rep('.crop-editor-iframe,\n.crop-editor-video {',
    '.crop-editor-video {')


# ========== 3. JS 变量注入 ==========
rep('''let localScreenStreams = { 1: null, 2: null };
// 已保存的直播来源；liveRoomIds 保留给旧配置兼容
let liveRoomIds = { 1: '', 2: '' };
let liveSources = { 1: createEmptyLiveSource(), 2: createEmptyLiveSource() };''',
    '''let localScreenStreams = { 1: null, 2: null };
// 已保存的直播来源；liveRoomIds 保留给旧配置兼容
let liveRoomIds = { 1: '', 2: '' };
let liveSources = { 1: createEmptyLiveSource(), 2: createEmptyLiveSource() };
// P4: 网络流播放器实例与类型
let liveStreamPlayers = { 1: null, 2: null };
let liveStreamKinds = { 1: '', 2: '' };''')


# ========== 4. JS 新增网络流工具函数 ==========
NETWORK_UTILS = r'''
// ---------- P4 网络流播放工具 ----------
function isGoBound() {
  return !!(window.go && window.go.app && window.go.app.App && typeof window.go.app.App.ResolveLive === 'function');
}

function stopNetworkStream(side) {
  liveStreamKinds[side] = '';
  const video = document.getElementById('liveVideo' + side);
  if (video) {
    video.pause();
    video.removeAttribute('src');
    video.src = '';
    video.load();
    video.classList.add('hide');
  }
  const old = liveStreamPlayers[side];
  if (old) {
    try { old.destroy(); } catch (e) {}
    liveStreamPlayers[side] = null;
  }
}

async function loadStream(side, source) {
  if (side !== 1 && side !== 2) return;
  if (!source || !source.type) return;
  if (source.type === 'local') {
    stopNetworkStream(side);
    return;
  }
  const video = document.getElementById('liveVideo' + side);
  const localVideo = document.getElementById('localVideo' + side);
  const tip = document.getElementById('tip' + side);
  if (localVideo) {
    localVideo.pause();
    localVideo.srcObject = null;
    localVideo.classList.add('hide');
  }
  if (video) video.classList.remove('hide');
  if (tip) tip.classList.add('hide');

  if (!isGoBound()) {
    setLiveTip(side, '未连接到桌面后端', '请通过 Wails 应用打开');
    return;
  }
  try {
    const res = await window.go.app.App.ResolveLive(side, source.type + ':' + source.value);
    liveStreamKinds[side] = res.kind || 'hls';

    const old = liveStreamPlayers[side];
    if (old) { try { old.destroy(); } catch (e) {} liveStreamPlayers[side] = null; }

    if (res.kind === 'flv' && typeof flvjs !== 'undefined' && flvjs.isSupported()) {
      const player = flvjs.createPlayer({
        type: 'flv',
        url: '/live/stream/' + side,
        isLive: true,
        hasAudio: true,
        hasVideo: true,
        cors: false
      });
      liveStreamPlayers[side] = player;
      player.attachMediaElement(video);
      player.load();
      player.play();
    } else if (res.kind === 'hls') {
      if (typeof Hls !== 'undefined' && Hls.isSupported()) {
        const player = new Hls({ enableWorker: true, liveSyncDurationCount: 3 });
        liveStreamPlayers[side] = player;
        player.loadSource('/live/stream/' + side);
        player.attachMedia(video);
        player.on(Hls.Events.MANIFEST_PARSED, () => { video.play().catch(() => {}); });
        player.on(Hls.Events.ERROR, (e, d) => {
          if (d.fatal) {
            console.error('HLS fatal error', d);
            showToast('直播加载失败：' + (d.type || 'unknown'));
          }
        });
      } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = '/live/stream/' + side;
        video.play().catch(() => {});
      } else {
        setLiveTip(side, '当前浏览器不支持 HLS 播放', '');
        return;
      }
    } else {
      video.src = '/live/stream/' + side;
      video.play().catch(() => {});
    }
    applyLiveCropToMain(side);
  } catch (e) {
    console.error('ResolveLive error', e);
    setLiveTip(side, '取流失败', e && e.message ? e.message : '');
  }
}
// ---------- P4 网络流播放工具结束 ----------

'''

rep('function bilibiliLiveEmbedUrl(roomId) {\n  return `https://www.bilibili.com/blackboard/live/live-activity-player.html?cid=${roomId}`;\n}\n',
    NETWORK_UTILS + 'function bilibiliLiveEmbedUrl(roomId) {\n  return `https://www.bilibili.com/blackboard/live/live-activity-player.html?cid=${roomId}`;\n}\n')


# ========== 5. JS 函数替换 ==========
rep('''function resolveLiveEmbedUrl(side) {
  const frame = document.getElementById('frame' + side);
  if (frame) {
    const attr = (frame.getAttribute('src') || '').trim();
    if (isValidLiveEmbedUrl(attr)) return attr;
    const srcProp = (frame.src || '').trim();
      if (isValidLiveEmbedUrl(srcProp)) return srcProp;
  }
  const source = normalizeLiveSource(liveSources[side]);
  if (source.type === 'bilibili') return bilibiliLiveEmbedUrl(source.value);
  return '';
}''', '''function resolveLiveEmbedUrl(side) {
  if (isSideUsingLocalVideo(side)) return '';
  const video = document.getElementById('liveVideo' + side);
  if (video && video.src && !video.classList.contains('hide')) return video.src;
  return liveStreamKinds[side] ? '/live/stream/' + side : '';
}''')

rep('''function applyLiveCropToMain(side) {
  const frame = document.getElementById('frame' + side);
  const zoom = document.getElementById('liveCropZoom' + side);
  if (!frame) return;
  const r = liveCrop[side];
  if (!r) return;
  if (r.w >= 99.5 && r.h >= 99.5 && r.l <= 0.25 && r.t <= 0.25) {
    frame.style.clipPath = '';
    if (zoom) {
      zoom.style.left = '';
      zoom.style.top = '';
      zoom.style.width = '';
      zoom.style.height = '';
    }
    return;
  }
  const w = Math.max(5, r.w);
  const h = Math.max(5, r.h);
  frame.style.clipPath = 'none';
  if (zoom) {
    zoom.style.left = (-100 * r.l / w) + '%';
    zoom.style.top = (-100 * r.t / h) + '%';
    zoom.style.width = (10000 / w) + '%';
    zoom.style.height = (10000 / h) + '%';
  } else {
    const insetTop = r.t;
    const insetRight = 100 - r.l - r.w;
    const insetBottom = 100 - r.t - r.h;
    const insetLeft = r.l;
    frame.style.clipPath = `inset(${insetTop}% ${insetRight}% ${insetBottom}% ${insetLeft}%)`;
  }
}''', '''function applyLiveCropToMain(side) {
  const frame = document.getElementById('liveVideo' + side);
  const localVideo = document.getElementById('localVideo' + side);
  const zoom = document.getElementById('liveCropZoom' + side);
  if (!frame && !localVideo) return;
  const r = liveCrop[side];
  if (!r) return;
  if (r.w >= 99.5 && r.h >= 99.5 && r.l <= 0.25 && r.t <= 0.25) {
    if (frame) frame.style.clipPath = '';
    if (localVideo) localVideo.style.clipPath = '';
    if (zoom) {
      zoom.style.left = '';
      zoom.style.top = '';
      zoom.style.width = '';
      zoom.style.height = '';
    }
    return;
  }
  const w = Math.max(5, r.w);
  const h = Math.max(5, r.h);
  if (frame) frame.style.clipPath = 'none';
  if (localVideo) localVideo.style.clipPath = 'none';
  if (zoom) {
    zoom.style.left = (-100 * r.l / w) + '%';
    zoom.style.top = (-100 * r.t / h) + '%';
    zoom.style.width = (10000 / w) + '%';
    zoom.style.height = (10000 / h) + '%';
  } else {
    const insetTop = r.t;
    const insetRight = 100 - r.l - r.w;
    const insetBottom = 100 - r.t - r.h;
    const insetLeft = r.l;
    if (frame) frame.style.clipPath = `inset(${insetTop}% ${insetRight}% ${insetBottom}% ${insetLeft}%)`;
    if (localVideo) localVideo.style.clipPath = `inset(${insetTop}% ${insetRight}% ${insetBottom}% ${insetLeft}%)`;
  }
}''')

rep('''function resetCropEditorPreview() {
  const ed = document.getElementById('cropEditorFrame');
  const edVideo = document.getElementById('cropEditorVideo');
  if (edVideo) {
    edVideo.pause();
    edVideo.srcObject = null;
    edVideo.classList.add('hide');
  }
  if (ed) {
    ed.src = 'about:blank';
    ed.classList.remove('hide');
  }
}''', '''function resetCropEditorPreview() {
  const edVideo = document.getElementById('cropEditorVideo');
  if (edVideo) {
    edVideo.pause();
    edVideo.srcObject = null;
    edVideo.removeAttribute('src');
    edVideo.src = '';
    edVideo.classList.add('hide');
  }
}''')

rep('''    if (localActive && edVideo) {
      if (ed) {
        ed.classList.add('hide');
        ed.src = 'about:blank';
      }
      const srcVideo = document.getElementById('localVideo' + n);
      edVideo.srcObject = srcVideo && srcVideo.srcObject ? srcVideo.srcObject : null;
      edVideo.classList.remove('hide');
    } else if (ed) {
      resetCropEditorPreview();
      ed.classList.remove('hide');
      ed.src = embedUrl;
    }''', '''    if (localActive && edVideo) {
      const srcVideo = document.getElementById('localVideo' + n);
      edVideo.srcObject = srcVideo && srcVideo.srcObject ? srcVideo.srcObject : null;
      edVideo.classList.remove('hide');
    } else if (edVideo) {
      resetCropEditorPreview();
      edVideo.classList.remove('hide');
      edVideo.src = embedUrl || '/live/stream/' + n;
    }''')

rep('''function hasVisibleLiveContent(side) {
  const video = document.getElementById('localVideo' + side);
  if (video && !video.classList.contains('hide') && video.srcObject) return true;
  const frame = document.getElementById('frame' + side);
  if (!frame || frame.classList.contains('hide')) return false;
  const src = (frame.getAttribute('src') || frame.src || '').trim();
  return !!src && !/^about:blank$/i.test(src);
}''', '''function hasVisibleLiveContent(side) {
  const localVideo = document.getElementById('localVideo' + side);
  if (localVideo && !localVideo.classList.contains('hide') && localVideo.srcObject) return true;
  const video = document.getElementById('liveVideo' + side);
  if (!video || video.classList.contains('hide')) return false;
  const src = (video.getAttribute('src') || video.src || '').trim();
  return !!src && !/^about:blank$/i.test(src);
}''')

rep('''function restoreLiveFramesFromSaved() {
  [1, 2].forEach((side) => {
    if (isSideUsingLocalVideo(side)) return;
    const source = normalizeLiveSource(liveSources[side]);
    if (!source.type) return;
    const frame = document.getElementById('frame' + side);
    const tip = document.getElementById('tip' + side);
    if (!frame) return;
    if (source.type === 'bilibili') {
      frame.classList.remove('hide');
      frame.src = bilibiliLiveEmbedUrl(source.value);
      if (tip) tip.classList.add('hide');
      applyLiveCropToMain(side);
    } else if (source.type === 'douyin') {
      frame.src = 'about:blank';
      frame.classList.add('hide');
      setLiveTip(side, '已保存抖音直播网址', '点击直播设置后重新采集画面');
    }
  });
}''', '''function restoreLiveFramesFromSaved() {
  [1, 2].forEach((side) => {
    if (isSideUsingLocalVideo(side)) return;
    const source = normalizeLiveSource(liveSources[side]);
    if (!source.type) return;
    loadStream(side, source);
  });
}''')

rep('''function confirmLiveId() {
  const sourceInput = document.getElementById('liveIdInput').value.trim();
  const name = document.getElementById('streamerName').value.trim();
  const source = parseLiveSource(sourceInput);
  if(!source) {showToast('请输入有效的B站房间号或抖音直播网址'); return;}
  setLiveSource(currentLiveNum, source);
  saveLiveSources();
  applyStreamerNameToTeam(currentLiveNum, name);
  const frame = document.getElementById(`frame${currentLiveNum}`);
  const tip = document.getElementById(`tip${currentLiveNum}`);
  if (source.type === 'bilibili') {
    stopLocalScreen(currentLiveNum);
    frame.classList.remove('hide');
    frame.src = bilibiliLiveEmbedUrl(source.value);
    tip.classList.add('hide');
    hideLiveModal();
    applyLiveCropToMain(currentLiveNum);
    showToast(`${currentLiveNum===1?'左':'右'}直播间已设置`);
    return;
  }
  hideLiveModal();
  const opened = openLiveSourcePage(source.value);
  if (!hasVisibleLiveContent(currentLiveNum)) {
    setLiveTip(currentLiveNum, '已保存抖音直播网址', '请在采集选择器中选择该页面');
  }
  captureLocalScreen(currentLiveNum, {
    promptMessage: opened
      ? '已打开抖音页面，请选择刚打开的窗口或标签页'
      : '请手动打开该抖音网址，并在采集选择器中选择对应窗口或标签页',
    cancelMessage: '已取消采集，抖音网址已保存，原画面保持不变'
  });
}''', '''function confirmLiveId() {
  const sourceInput = document.getElementById('liveIdInput').value.trim();
  const name = document.getElementById('streamerName').value.trim();
  const source = parseLiveSource(sourceInput);
  if(!source) {showToast('请输入有效的B站房间号或抖音直播网址'); return;}
  setLiveSource(currentLiveNum, source);
  saveLiveSources();
  applyStreamerNameToTeam(currentLiveNum, name);
  stopLocalScreen(currentLiveNum);
  hideLiveModal();
  loadStream(currentLiveNum, source);
  showToast(`${currentLiveNum===1?'左':'右'}直播间已设置`);
}''')

rep('''function stopLocalScreen(side) {
  const stream = localScreenStreams[side];
  if (stream) {
    stream.getTracks().forEach((t) => t.stop());
    localScreenStreams[side] = null;
  }
  const video = document.getElementById('localVideo' + side);
  const frame = document.getElementById('frame' + side);
  if (video) {
    video.srcObject = null;
    video.classList.add('hide');
  }
  if (frame) frame.classList.remove('hide');
}''', '''function stopLocalScreen(side) {
  const stream = localScreenStreams[side];
  if (stream) {
    stream.getTracks().forEach((t) => t.stop());
    localScreenStreams[side] = null;
  }
  const video = document.getElementById('localVideo' + side);
  const frame = document.getElementById('liveVideo' + side);
  if (video) {
    video.pause();
    video.srcObject = null;
    video.classList.add('hide');
  }
  if (frame) frame.classList.remove('hide');
}''')

rep('''    const frame = document.getElementById('frame' + side);
    const video = document.getElementById('localVideo' + side);
    const tip = document.getElementById('tip' + side);
    if (frame) {
      frame.src = 'about:blank';
      frame.classList.add('hide');
    }''', '''    stopNetworkStream(side);
    const frame = document.getElementById('liveVideo' + side);
    const video = document.getElementById('localVideo' + side);
    const tip = document.getElementById('tip' + side);
    if (frame) {
      frame.pause();
      frame.removeAttribute('src');
      frame.src = '';
      frame.classList.add('hide');
    }''')

rep('''        const t = document.getElementById('tip' + side);
        const f = document.getElementById('frame' + side);
        if (f && (!f.src || f.src === 'about:blank')) {
          if (t) t.classList.remove('hide');
        }''', '''        const t = document.getElementById('tip' + side);
        const f = document.getElementById('liveVideo' + side);
        if (f && (!f.src || f.src === 'about:blank' || f.src.endsWith('/live/stream/' + side))) {
          if (t) t.classList.remove('hide');
        }''')


# ========== 6. 写入 ==========
with open(HTML, 'w', encoding='utf-8') as f:
    f.write(s)

print(f'P4 patch applied. {orig_len} -> {len(s)} bytes (+{len(s)-orig_len})')
