'use strict';

const q = (sel) => document.querySelector(sel);

const el = {
  status: q('#status'),
  statusText: q('#status-text'),
  drop: q('#drop'),
  fileInput: q('#file-input'),
  fileEmpty: q('#file-empty'),
  fileView: q('#file-view'),
  fileName: q('#file-name'),
  fileMeta: q('#file-meta'),
  download: q('#download'),
  replace: q('#replace'),
  deleteFile: q('#delete-file'),
  progressWrap: q('#progress-wrap'),
  progress: q('#progress'),
  progressText: q('#progress-text'),
  tabs: q('#tabs'),
  newText: q('#new-text'),
  title: q('#text-title'),
  content: q('#text-content'),
  textMeta: q('#text-meta'),
  charCount: q('#char-count'),
  copyText: q('#copy-text'),
  deleteText: q('#delete-text'),
  cmdList: q('#cmd-list'),
  fileLimit: q('#file-limit'),
  toast: q('#toast'),
};

const state = {
  file: null,
  texts: [],
  activeId: null,
  maxUpload: 0,
  maxText: 0,
  lastUpdated: '',
  dirty: false,
  saveFailed: false,
  editVersion: 0,
  saveTimer: null,
  uploading: false,
};

let saveChain = Promise.resolve();
let toastTimer = null;

function setOnline(online) {
  el.status.classList.toggle('online', online);
  el.status.classList.toggle('offline', !online);
  el.statusText.textContent = online ? '已连接' : '连接断开';
}

async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers || {});
  if (opts.body && typeof opts.body === 'string' && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }

  let res;
  try {
    res = await fetch(path, Object.assign({}, opts, { headers }));
  } catch (err) {
    setOnline(false);
    throw new Error('网络连接失败');
  }
  setOnline(true);

  const type = res.headers.get('content-type') || '';
  let data = null;
  if (type.includes('application/json')) {
    try {
      data = await res.json();
    } catch (err) {
      data = null;
    }
  }
  if (!res.ok) {
    throw new Error((data && data.error) || ('请求失败（' + res.status + '）'));
  }
  return data;
}

function toast(message, isError = false) {
  el.toast.textContent = message;
  el.toast.classList.toggle('error', isError);
  el.toast.hidden = false;
  requestAnimationFrame(() => el.toast.classList.add('show'));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.toast.classList.remove('show');
    setTimeout(() => {
      el.toast.hidden = true;
    }, 220);
  }, 2400);
}

function fmtSize(n) {
  if (!n && n !== 0) return '';
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + ' KB';
  return n + ' B';
}

function fmtTime(value) {
  if (!value) return '';
  const d = new Date(value);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString('zh-CN', {
    hour12: false,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (err) {
      /* 回退到 execCommand */
    }
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.position = 'fixed';
  ta.style.left = '-9999px';
  ta.style.top = '0';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch (err) {
    ok = false;
  }
  ta.remove();
  return ok;
}

/* ---------- 文件槽 ---------- */

function renderFile() {
  const f = state.file;
  if (!f) {
    el.fileEmpty.hidden = false;
    el.fileView.hidden = true;
    el.download.href = '/api/download';
    return;
  }
  el.fileEmpty.hidden = true;
  el.fileView.hidden = false;
  el.fileName.textContent = f.name || 'file.bin';
  el.fileMeta.textContent = fmtSize(f.size) + ' · ' + fmtTime(f.updated);
  el.download.href = '/api/download?t=' + encodeURIComponent(f.updated || '');
}

function renderLimits() {
  el.fileLimit.textContent = state.maxUpload ? '单个文件上限 ' + fmtSize(state.maxUpload) : '';
  if (state.maxText) {
    el.content.maxLength = state.maxText;
  }
}

function updateButtons() {
  el.replace.disabled = state.uploading;
  el.deleteFile.disabled = state.uploading;
  el.drop.style.pointerEvents = state.uploading ? 'none' : '';
}

function uploadFile(file) {
  if (!file || state.uploading) return;
  if (state.maxUpload && file.size > state.maxUpload) {
    toast('文件超过上限 ' + fmtSize(state.maxUpload), true);
    return;
  }

  const form = new FormData();
  form.append('file', file, file.name || 'upload.bin');

  state.uploading = true;
  updateButtons();
  el.progressWrap.hidden = false;
  el.progress.value = 0;
  el.progressText.textContent = '0%';

  const xhr = new XMLHttpRequest();
  xhr.open('POST', '/api/upload');
  xhr.responseType = 'text';

  xhr.upload.onprogress = (ev) => {
    if (!ev.lengthComputable) return;
    const pct = Math.min(100, Math.round((ev.loaded / ev.total) * 100));
    el.progress.value = pct;
    el.progressText.textContent = pct + '%';
  };

  xhr.onload = () => {
    let data = null;
    try {
      data = JSON.parse(xhr.responseText || 'null');
    } catch (err) {
      data = null;
    }
    if (xhr.status >= 200 && xhr.status < 300 && data) {
      state.file = data;
      renderFile();
      el.progress.value = 100;
      el.progressText.textContent = '100%';
      toast('上传完成：' + (data.name || '文件'));
      setTimeout(() => {
        el.progressWrap.hidden = true;
      }, 700);
    } else {
      toast((data && data.error) || ('上传失败（' + xhr.status + '）'), true);
      el.progressWrap.hidden = true;
    }
  };

  xhr.onerror = () => {
    toast('上传失败，网络中断', true);
    el.progressWrap.hidden = true;
  };

  xhr.onloadend = () => {
    state.uploading = false;
    updateButtons();
  };

  xhr.send(form);
}

el.drop.addEventListener('click', () => el.fileInput.click());
el.drop.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter' || ev.key === ' ') {
    ev.preventDefault();
    el.fileInput.click();
  }
});
el.fileInput.addEventListener('change', () => {
  if (el.fileInput.files && el.fileInput.files[0]) {
    uploadFile(el.fileInput.files[0]);
    el.fileInput.value = '';
  }
});
el.replace.addEventListener('click', () => el.fileInput.click());

el.deleteFile.addEventListener('click', async () => {
  if (!confirm('删除当前文件？')) return;
  try {
    await api('/api/file', { method: 'DELETE' });
    state.file = null;
    renderFile();
    toast('文件已删除');
  } catch (err) {
    toast(err.message, true);
  }
});

['dragenter', 'dragover'].forEach((type) => {
  document.addEventListener(type, (ev) => {
    if (ev.dataTransfer && Array.from(ev.dataTransfer.types || []).includes('Files')) {
      ev.preventDefault();
      el.drop.classList.add('over');
    }
  });
});

document.addEventListener('dragleave', (ev) => {
  if (!ev.relatedTarget) el.drop.classList.remove('over');
});

document.addEventListener('drop', (ev) => {
  if (ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files.length) {
    ev.preventDefault();
    el.drop.classList.remove('over');
    uploadFile(ev.dataTransfer.files[0]);
  }
});

document.addEventListener('paste', (ev) => {
  const items = ev.clipboardData ? ev.clipboardData.items : null;
  if (!items) return;
  for (const item of items) {
    if (item.kind === 'file') {
      const file = item.getAsFile();
      if (file) {
        ev.preventDefault();
        uploadFile(file);
      }
      break;
    }
  }
});

/* ---------- 文字窗口 ---------- */

function renderTabs() {
  el.tabs.innerHTML = '';
  for (const t of state.texts) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'tab' + (t.id === state.activeId ? ' active' : '');
    btn.textContent = t.title || ('窗口 ' + t.id);
    btn.title = btn.textContent;
    btn.setAttribute('role', 'tab');
    btn.setAttribute('aria-selected', t.id === state.activeId ? 'true' : 'false');
    btn.addEventListener('click', () => {
      void selectText(t.id);
    });
    el.tabs.appendChild(btn);
  }
}

function updateCount() {
  el.charCount.textContent = el.content.value.length + ' 字';
}

async function loadText(id) {
  const version = state.editVersion;
  const data = await api('/api/texts/' + id);
  if (state.activeId !== id || state.editVersion !== version) return;

  state.dirty = false;
  state.saveFailed = false;
  state.lastUpdated = data.updated || '';
  state.editVersion++;
  el.title.value = data.title || '';
  el.content.value = data.content || '';
  updateCount();
  el.textMeta.textContent = '已保存 ' + fmtTime(data.updated);
}

async function selectText(id) {
  if (id === state.activeId) return;
  clearTimeout(state.saveTimer);
  const ok = await flushSave();
  if (!ok) return;
  state.activeId = id;
  renderTabs();
  try {
    await loadText(id);
  } catch (err) {
    toast(err.message, true);
  }
}

function scheduleSave() {
  state.dirty = true;
  state.saveFailed = false;
  state.editVersion++;
  clearTimeout(state.saveTimer);
  state.saveTimer = setTimeout(() => {
    void flushSave();
  }, 700);
}

async function doSave() {
  if (!state.dirty || state.activeId == null) return true;
  const id = state.activeId;
  const version = state.editVersion;
  const content = el.content.value;
  const title = el.title.value;

  try {
    const data = await api('/api/texts/' + id, {
      method: 'PUT',
      body: JSON.stringify({ content, title }),
    });
    if (state.activeId === id) {
      state.lastUpdated = data.updated || '';
      if (state.editVersion === version) {
        state.dirty = false;
        el.textMeta.textContent = '已保存 ' + fmtTime(data.updated);
      }
      const item = state.texts.find((t) => t.id === id);
      if (item && data.title) {
        item.title = data.title;
        renderTabs();
      }
    }
    state.saveFailed = false;
    return true;
  } catch (err) {
    state.saveFailed = true;
    toast(err.message || '保存失败', true);
    return false;
  }
}

function flushSave() {
  clearTimeout(state.saveTimer);
  const p = saveChain.then(async () => {
    let ok = true;
    while (state.dirty && !state.saveFailed) {
      ok = await doSave();
    }
    return ok && !state.saveFailed;
  });
  saveChain = p.then(
    () => {},
    () => {}
  );
  return p;
}

el.content.addEventListener('input', () => {
  updateCount();
  scheduleSave();
});
el.title.addEventListener('input', scheduleSave);

el.newText.addEventListener('click', async () => {
  if (!(await flushSave())) return;
  try {
    const meta = await api('/api/texts', { method: 'POST', body: '{}' });
    state.texts.push(meta);
    state.activeId = meta.id;
    renderTabs();
    await loadText(meta.id);
    el.content.focus();
  } catch (err) {
    toast(err.message, true);
  }
});

el.copyText.addEventListener('click', async () => {
  if (await copyText(el.content.value)) {
    toast('已复制到剪贴板');
  } else {
    toast('复制失败，请手动选择', true);
  }
});

el.deleteText.addEventListener('click', async () => {
  if (state.activeId == null) return;
  if (!confirm('删除这个文字窗口？内容会一起删除。')) return;

  const id = state.activeId;
  clearTimeout(state.saveTimer);
  state.dirty = false;
  state.saveFailed = false;
  state.editVersion++;
  await saveChain.catch(() => {});

  try {
    await api('/api/texts/' + id, { method: 'DELETE' });
    state.texts = state.texts.filter((t) => t.id !== id);
    state.activeId = state.texts.length ? state.texts[0].id : null;
    renderTabs();
    if (state.activeId != null) {
      await loadText(state.activeId);
    } else {
      el.title.value = '';
      el.content.value = '';
      updateCount();
      el.textMeta.textContent = '';
    }
    toast('窗口已删除');
  } catch (err) {
    toast(err.message, true);
  }
});

/* ---------- 命令行 ---------- */

function renderCommands() {
  const base = location.origin;
  const firstId = state.texts.length ? state.texts[0].id : 1;
  const cmds = [
    { label: '上传文件（保留原文件名）', cmd: 'curl -T 本地文件 ' + base + '/api/upload' },
    { label: '上传并指定文件名', cmd: 'curl -T 本地文件 "' + base + '/api/upload?name=backup.tar.gz"' },
    { label: '下载当前文件', cmd: 'curl -fsSL ' + base + '/api/download -o backup.bin' },
    {
      label: '写入文字窗口 ' + firstId,
      cmd: "printf '要传的文字' | curl --data-binary @- -H 'Content-Type: text/plain' -X PUT " + base + '/api/texts/' + firstId,
    },
    { label: '读取文字窗口 ' + firstId, cmd: 'curl -fsSL ' + base + '/api/texts/' + firstId },
    { label: '健康检查', cmd: 'curl -fsSL ' + base + '/api/health' },
  ];

  el.cmdList.innerHTML = '';
  for (const item of cmds) {
    const box = document.createElement('div');
    box.className = 'cmd';

    const top = document.createElement('div');
    top.className = 'cmd-top';

    const label = document.createElement('span');
    label.className = 'cmd-label';
    label.textContent = item.label;

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn mini';
    btn.textContent = '复制';
    btn.addEventListener('click', async () => {
      if (await copyText(item.cmd)) {
        toast('命令已复制');
      } else {
        toast('复制失败，请手动选择', true);
      }
    });

    top.appendChild(label);
    top.appendChild(btn);

    const pre = document.createElement('pre');
    const code = document.createElement('code');
    code.textContent = item.cmd;
    pre.appendChild(code);

    box.appendChild(top);
    box.appendChild(pre);
    el.cmdList.appendChild(box);
  }
}

/* ---------- 状态同步 ---------- */

async function refreshState() {
  const data = await api('/api/state');
  const prevActive = state.activeId;

  state.file = data.file || null;
  state.texts = data.texts || [];
  state.maxUpload = data.max_upload || 0;
  state.maxText = data.max_text || 0;

  renderFile();
  renderLimits();
  renderTabs();
  renderCommands();

  if (prevActive == null || !state.texts.some((t) => t.id === prevActive)) {
    state.activeId = state.texts.length ? state.texts[0].id : null;
    renderTabs();
    if (state.activeId != null) await loadText(state.activeId);
  }
}

async function poll() {
  if (document.hidden || state.uploading || state.dirty) return;
  const activeId = state.activeId;

  try {
    const data = await api('/api/state');
    state.file = data.file || null;
    state.texts = data.texts || [];
    state.maxUpload = data.max_upload || 0;
    state.maxText = data.max_text || 0;
    renderFile();
    renderLimits();
    renderTabs();

    if (activeId != null && !state.texts.some((t) => t.id === activeId) && state.texts.length) {
      state.activeId = state.texts[0].id;
      renderTabs();
      await loadText(state.activeId);
      return;
    }

    const item = state.texts.find((t) => t.id === state.activeId);
    if (
      item &&
      !state.dirty &&
      item.updated &&
      item.updated !== state.lastUpdated &&
      document.activeElement !== el.content &&
      document.activeElement !== el.title
    ) {
      await loadText(state.activeId);
    }
  } catch (err) {
    /* 轮询失败不打扰用户 */
  }
}

async function boot() {
  renderCommands();
  try {
    await refreshState();
  } catch (err) {
    toast(err.message, true);
  }
  setInterval(() => {
    void poll();
  }, 6000);
}

window.addEventListener('pagehide', () => {
  if (!state.dirty || state.activeId == null) return;
  fetch('/api/texts/' + state.activeId, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content: el.content.value, title: el.title.value }),
    keepalive: true,
  }).catch(() => {});
});

void boot();









