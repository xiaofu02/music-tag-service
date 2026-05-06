(function() {
    const API = '/api/v1';
    let currentPath = '';
    let currentTag = null;
    let currentFilePath = '';
    let currentCoverBase64 = '';
    let coverRemoved = false;
    let allFiles = [];
    let autoTagPaths = [];

    const providerNames = { netease: '网易云', qmusic: 'QQ音乐', kugou: '酷狗', kuwo: '酷我' };
    const $ = id => document.getElementById(id);

    function init() {
        checkHealth();
        loadFolder('');

        $('btn-go-path').addEventListener('click', () => loadFolder($('path-input').value));
        $('btn-go-up').addEventListener('click', () => {
            if (currentPath) {
                const parts = currentPath.replace(/\\/g, '/').split('/').filter(Boolean);
                parts.pop();
                loadFolder(parts.length > 0 ? parts.join('/') : '');
            }
        });
        $('path-input').addEventListener('keydown', e => { if (e.key === 'Enter') loadFolder($('path-input').value); });
        $('file-search').addEventListener('input', filterFiles);
        $('sort-select').addEventListener('change', () => loadFolder(currentPath));
        $('btn-save').addEventListener('click', saveTag);
        $('btn-search').addEventListener('click', doSearch);
        $('search-keyword').addEventListener('keydown', e => { if (e.key === 'Enter') doSearch(); });
        $('cover-file-input').addEventListener('change', handleCoverUpload);
        $('btn-remove-cover').addEventListener('click', removeCover);
        $('btn-run-auto-tag').addEventListener('click', runAutoTag);
        $('btn-run-organize').addEventListener('click', runOrganize);
        $('btn-batch-close').addEventListener('click', () => $('modal-batch-progress').style.display = 'none');
        $('btn-run-batch-rename').addEventListener('click', runBatchRename);
        $('btn-settings').addEventListener('click', () => { $('modal-settings').style.display = 'flex'; loadSettingsData(); });
        $('btn-save-settings').addEventListener('click', saveSettings);
        $('btn-start-auto-import').addEventListener('click', startAutoImport);
        $('btn-stop-auto-import').addEventListener('click', stopAutoImport);

        $('btn-browse-import-path')?.addEventListener('click', async () => {
            try {
                const data = await api('GET', '/browse-folder');
                if (data.data && data.data.path) {
                    $('auto-import-path').value = data.data.path;
                }
            } catch(e) {
                toast('选择文件夹失败: ' + e.message, 'error');
            }
        });

        document.querySelectorAll('.btn-search-field').forEach(btn => {
            btn.addEventListener('click', () => {
                const val = $('f-title').value;
                if (val) { $('search-keyword').value = val; doSearch(); }
            });
        });
    }

    async function api(method, path, body) {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (body) opts.body = JSON.stringify(body);
        const resp = await fetch(API + path, opts);
        const data = await resp.json();
        if (!data.ok) throw new Error(data.error || '未知错误');
        return data;
    }

    async function checkHealth() {
        try {
            await api('GET', '/health');
            const el = $('status-text');
            if (el) { el.textContent = '已连接'; el.style.background = 'var(--success)'; }
        } catch(e) {
            const el = $('status-text');
            if (el) { el.textContent = '已断开'; el.style.background = 'var(--error)'; }
        }
    }

    async function loadFolder(path) {
        currentPath = path;
        const treeEl = $('file-tree');
        treeEl.innerHTML = '<div class="empty-state">加载中...</div>';

        try {
            const url = path ? `/folder?path=${encodeURIComponent(path)}` : '/folder';
            const data = await api('GET', url);
            const folder = data.data;
            $('path-input').value = folder.path;
            allFiles = folder.items || [];
            renderFileTree(allFiles);
        } catch(e) {
            treeEl.innerHTML = `<div class="empty-state" style="color:var(--error)">${e.message}</div>`;
        }
    }

    function filterFiles() {
        const q = $('file-search').value.toLowerCase();
        const filtered = q ? allFiles.filter(f => f.name.toLowerCase().includes(q)) : allFiles;
        renderFileTree(filtered);
    }

    function renderFileTree(items) {
        const treeEl = $('file-tree');
        if (!items || items.length === 0) {
            treeEl.innerHTML = '<div class="empty-state">空文件夹</div>';
            return;
        }

        const folderIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>';
        const audioIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>';
        const fileIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/></svg>';

        treeEl.innerHTML = items.map(item => {
            const icon = item.is_dir ? folderIcon : (item.is_audio ? audioIcon : fileIcon);
            const size = item.is_dir ? '' : formatSize(item.size);
            return `<div class="file-item" data-path="${escapeAttr(item.path)}" data-is-dir="${item.is_dir}" data-is-audio="${item.is_audio}">
                <span class="icon">${icon}</span>
                <span class="name">${escapeHtml(item.name)}</span>
                <span class="size">${size}</span>
            </div>`;
        }).join('');

        treeEl.querySelectorAll('.file-item').forEach(el => {
            el.addEventListener('click', () => {
                const path = el.dataset.path;
                const isDir = el.dataset.isDir === 'true';
                const isAudio = el.dataset.isAudio === 'true';
                if (isDir) {
                    loadFolder(path);
                } else if (isAudio) {
                    loadTag(path);
                    treeEl.querySelectorAll('.file-item').forEach(s => s.classList.remove('active'));
                    el.classList.add('active');
                }
            });
        });
    }

    async function loadTag(path) {
        currentFilePath = path;
        currentCoverBase64 = '';
        coverRemoved = false;
        $('edit-welcome').style.display = 'none';
        $('edit-form').style.display = 'block';

        try {
            const data = await api('GET', `/tag?path=${encodeURIComponent(path)}`);
            currentTag = data.data;
            renderTag(currentTag);
        } catch(e) {
            toast('读取失败: ' + e.message, 'error');
        }
    }

    function renderTag(t) {
        $('f-title').value = t.title || '';
        $('f-artist').value = t.artist || '';
        $('f-album').value = t.album || '';
        $('f-albumartist').value = t.album_artist || '';
        $('f-genre').value = t.genre || '';
        $('f-year').value = t.year || '';
        $('f-track').value = t.track_number ? (t.track_total ? `${t.track_number}/${t.track_total}` : t.track_number) : '';
        $('f-disc').value = t.disc_number ? (t.disc_total ? `${t.disc_number}/${t.disc_total}` : t.disc_number) : '';
        $('f-lyrics').value = t.lyrics || '';
        $('f-duration').value = formatDuration(t.duration);
        $('f-bitrate').value = t.bit_rate ? t.bit_rate + ' kbps' : '';
        $('f-filesize').value = formatSize(t.file_size);

        if (t.has_cover && t.cover_base64) {
            const mime = t.cover_mime || 'image/jpeg';
            $('f-cover-img').src = `data:${mime};base64,${t.cover_base64}`;
            $('f-cover-img').style.display = 'block';
            $('f-cover-placeholder').style.display = 'none';
            currentCoverBase64 = t.cover_base64;
        } else {
            $('f-cover-img').style.display = 'none';
            $('f-cover-placeholder').style.display = 'flex';
            currentCoverBase64 = '';
        }
        coverRemoved = false;
        $('search-keyword').value = t.title || (t.file_name || '').split('.')[0];
    }

    async function saveTag() {
        if (!currentFilePath) return;

        const trackParts = ($('f-track').value || '').split('/');
        const discParts = ($('f-disc').value || '').split('/');

        const req = {
            file_path: currentFilePath,
            title: $('f-title').value,
            artist: $('f-artist').value,
            album: $('f-album').value,
            album_artist: $('f-albumartist').value,
            genre: $('f-genre').value,
            year: parseInt($('f-year').value) || 0,
            track_number: parseInt(trackParts[0]) || 0,
            track_total: parseInt(trackParts[1]) || 0,
            disc_number: parseInt(discParts[0]) || 0,
            disc_total: parseInt(discParts[1]) || 0,
            lyrics: $('f-lyrics').value,
            cover_base64: coverRemoved ? '' : currentCoverBase64,
            remove_cover: coverRemoved,
        };

        try {
            await api('PUT', '/tag', req);
            toast('保存成功', 'success');
            await loadTag(currentFilePath);
        } catch(e) {
            toast('保存失败: ' + e.message, 'error');
        }
    }

    function handleCoverUpload(e) {
        const file = e.target.files[0];
        if (!file) return;
        const reader = new FileReader();
        reader.onload = function(ev) {
            currentCoverBase64 = ev.target.result.split(',')[1];
            coverRemoved = false;
            $('f-cover-img').src = ev.target.result;
            $('f-cover-img').style.display = 'block';
            $('f-cover-placeholder').style.display = 'none';
        };
        reader.readAsDataURL(file);
    }

    function removeCover() {
        coverRemoved = true;
        currentCoverBase64 = '';
        $('f-cover-img').style.display = 'none';
        $('f-cover-placeholder').style.display = 'flex';
    }

    async function doSearch() {
        const keyword = $('search-keyword').value.trim();
        const provider = $('search-provider').value;
        if (!keyword) return;

        const resultsEl = $('search-results');
        resultsEl.innerHTML = '<div class="empty-state">搜索中...</div>';

        try {
            let songs;
            if (provider === 'smart') {
                songs = await smartSearch(keyword);
            } else {
                const data = await api('GET', `/search?q=${encodeURIComponent(keyword)}&provider=${provider}`);
                songs = data.data.songs || [];
            }

            if (songs.length === 0) {
                resultsEl.innerHTML = '<div class="empty-state">未找到结果</div>';
                return;
            }

            resultsEl.innerHTML = songs.map(song => `
                <div class="search-result-item" data-song='${escapeAttr(JSON.stringify(song))}'>
                    ${song.album_img ? `<img class="result-cover" src="${song.album_img}" alt="" onerror="this.style.display='none'">` : ''}
                    <div class="result-info">
                        <div class="result-title">${escapeHtml(song.name)}</div>
                        <div class="result-meta">${escapeHtml(song.artist)} - ${escapeHtml(song.album)}${song.year ? ' (' + song.year + ')' : ''}</div>
                        <div class="result-provider">${providerNames[song.provider] || song.provider}</div>
                    </div>
                </div>
            `).join('');

            resultsEl.querySelectorAll('.search-result-item').forEach(el => {
                el.addEventListener('click', () => {
                    const song = JSON.parse(el.dataset.song);
                    if (currentFilePath) {
                        applySong(song);
                    } else {
                        fillFromSong(song);
                    }
                });
            });
        } catch(e) {
            resultsEl.innerHTML = `<div class="empty-state" style="color:var(--error)">${e.message}</div>`;
        }
    }

    async function smartSearch(keyword) {
        const providers = ['netease', 'qmusic', 'kugou', 'kuwo'];
        const promises = providers.map(p =>
            api('GET', `/search?q=${encodeURIComponent(keyword)}&provider=${p}`)
                .then(d => (d.data.songs || []).map(s => ({...s, provider: s.provider || p})))
                .catch(() => [])
        );
        const results = await Promise.all(promises);
        let all = results.flat();
        all.sort((a, b) => {
            const sa = matchScore(keyword, a.name) + matchScore(keyword, a.artist);
            const sb = matchScore(keyword, b.name) + matchScore(keyword, b.artist);
            return sb - sa;
        });
        return all.slice(0, 15);
    }

    function matchScore(a, b) {
        if (!a || !b) return 0;
        a = a.toLowerCase().replace(/\s/g, '');
        b = b.toLowerCase().replace(/\s/g, '');
        if (a === b) return 2;
        if (a.includes(b) || b.includes(a)) return 1;
        return 0;
    }

    function fillFromSong(song) {
        if (song.name) $('f-title').value = song.name;
        if (song.artist) $('f-artist').value = song.artist;
        if (song.album) $('f-album').value = song.album;
        if (song.year) $('f-year').value = parseInt(song.year) || '';
        toast('已填充来自' + (providerNames[song.provider] || song.provider) + '的信息', 'info');
    }

    async function applySong(song) {
        if (!currentFilePath) { toast('未选择文件', 'error'); return; }
        toast('正在应用...', 'info');
        try {
            const applyData = await api('POST', '/apply', {
                file_path: currentFilePath,
                title: song.name || '',
                artist: song.artist || '',
                album: song.album || '',
                year: parseInt(song.year) || 0,
                song_id: song.id || '',
                provider: song.provider || '',
                cover_url: song.album_img || '',
                fetch_lyric: true,
                save_cover_file: false,
                save_lyrics_file: false,
            });
            if (applyData.data.success) {
                toast('已应用来自' + (providerNames[song.provider] || song.provider) + '的信息', 'success');
                await loadTag(currentFilePath);
            } else {
                toast('应用失败: ' + (applyData.data.message || '未知错误'), 'error');
            }
        } catch(e) {
            toast('应用出错: ' + e.message, 'error');
        }
    }

    function openAutoTagModal(paths) {
        autoTagPaths = paths;
        const isDir = paths.length === 1 && !paths[0].match(/\.(mp3|flac|wav|aiff|m4a|ogg|opus|wma|ape|wv|tta|mpc|dsf|dff|aac)$/i);
        const scopeEl = $('auto-tag-scope');
        if (scopeEl) {
            if (isDir) scopeEl.textContent = '当前目录: ' + paths[0];
            else if (paths.length === 1) scopeEl.textContent = '单文件: ' + paths[0].split(/[/\\]/).pop();
            else scopeEl.textContent = `已选择 ${paths.length} 个文件/文件夹`;
        }
        $('modal-auto-tag').style.display = 'flex';
    }

    async function runAutoTag() {
        $('modal-auto-tag').style.display = 'none';

        const mode = $('auto-mode').value;
        const overwrite = $('auto-overwrite').checked;
        const concurrency = parseInt($('auto-concurrency').value) || 4;
        const providers = [];
        document.querySelectorAll('#modal-auto-tag .provider-item input:checked').forEach(cb => {
            providers.push(cb.value);
        });

        if (providers.length === 0) { toast('请至少选择一个音乐来源', 'error'); return; }

        showBatchProgress(autoTagPaths.length);

        try {
            const data = await api('POST', '/auto-tag', {
                paths: autoTagPaths,
                providers: providers,
                mode: mode,
                concurrency: concurrency,
                overwrite: overwrite,
                save_cover: false,
                save_lyrics: false,
            });

            const results = data.data.results || [];
            const total = data.data.total || results.length;
            const successCount = data.data.success_count || results.filter(r => r.success).length;
            const failCount = data.data.fail_count || results.filter(r => !r.success).length;

            updateBatchProgress(total, total, successCount, failCount, results);
            if (currentFilePath) await loadTag(currentFilePath);
        } catch(e) {
            updateBatchProgress(0, 0, 0, 0, []);
            toast('批量刮削出错: ' + e.message, 'error');
        }
    }

    function showBatchProgress(total) {
        $('modal-batch-progress').style.display = 'flex';
        $('batch-progress-bar').style.width = '0%';
        $('batch-progress-text').textContent = '处理中...';
        $('batch-success-count').textContent = '0';
        $('batch-fail-count').textContent = '0';
        $('batch-result-list').innerHTML = '<div class="empty-state">正在处理，请稍候...</div>';
        $('btn-batch-close').disabled = true;
    }

    function updateBatchProgress(done, total, successCount, failCount, results) {
        const pct = total > 0 ? Math.round(done / total * 100) : 0;
        $('batch-progress-bar').style.width = pct + '%';
        $('batch-progress-text').textContent = `${done} / ${total}`;
        $('batch-success-count').textContent = successCount;
        $('batch-fail-count').textContent = failCount;

        if (results.length > 0) {
            $('batch-result-list').innerHTML = results.map(r => {
                const icon = r.success ? '&#10003;' : '&#10007;';
                const color = r.success ? 'var(--success)' : 'var(--error)';
                const matchInfo = r.match ? ` → ${escapeHtml(r.match.artist || '')} - ${escapeHtml(r.match.album || '')}` : '';
                return `<div style="padding:4px 0;border-bottom:1px solid var(--border);font-size:12px;">
                    <span style="color:${color}">${icon}</span>
                    <b>${escapeHtml(r.file_name || (r.file_path || '').split(/[/\\]/).pop())}</b>
                    <span style="color:var(--text2)">${matchInfo}</span>
                    ${!r.success ? `<span style="color:var(--error);font-size:11px"> ${escapeHtml(r.message || '')}</span>` : ''}
                </div>`;
            }).join('');
        } else {
            $('batch-result-list').innerHTML = '<div class="empty-state">无结果</div>';
        }
        $('btn-batch-close').disabled = false;
    }

    async function runOrganize() {
        if (!currentFilePath) return;
        $('modal-organize').style.display = 'none';

        const rootPath = $('organize-root').value;
        const firstDir = $('organize-first').value;
        const secondDir = $('organize-second').value;

        toast('正在整理...', 'info');
        try {
            const data = await api('POST', '/organize', {
                paths: [currentFilePath],
                root_path: rootPath,
                first_dir: firstDir,
                second_dir: secondDir,
                dry_run: false,
            });
            toast('已整理 ' + data.data.total + ' 个文件', 'success');
            loadFolder(currentPath);
        } catch(e) {
            toast('整理出错: ' + e.message, 'error');
        }
    }

    function openBatchRenameModal() {
        const scopeEl = $('rename-scope');
        if (scopeEl) scopeEl.textContent = `当前目录: ${currentPath}`;
        $('modal-batch-rename').style.display = 'flex';
    }

    async function runBatchRename() {
        $('modal-batch-rename').style.display = 'none';
        const template = $('rename-template').value || '{artist} - {title}';

        const audioFiles = allFiles.filter(f => f.is_audio).map(f => f.path);
        if (audioFiles.length === 0) {
            toast('当前目录没有音频文件', 'error');
            return;
        }

        toast('正在重命名...', 'info');
        try {
            const data = await api('POST', '/batch-rename', {
                paths: audioFiles,
                template: template,
            });
            toast('已重命名 ' + data.data.total + ' 个文件', 'success');
            loadFolder(currentPath);
        } catch(e) {
            toast('重命名出错: ' + e.message, 'error');
        }
    }

    function toast(msg, type) {
        const el = $('toast');
        el.textContent = msg;
        el.className = 'toast ' + type;
        el.style.display = 'block';
        clearTimeout(el._timer);
        el._timer = setTimeout(() => { el.style.display = 'none'; }, 3000);
    }

    async function loadSettingsData() {
        try {
            const data = await api('GET', '/config');
            const cfg = data.data;

            $('auto-import-enabled').checked = cfg.auto_import.enabled;
            $('auto-import-path').value = cfg.auto_import.watch_path || '';
            $('auto-import-concurrency').value = cfg.auto_import.concurrency || 4;
            $('auto-import-mode').value = cfg.auto_import.mode || 'smart';
            $('auto-import-overwrite').checked = cfg.auto_import.overwrite;

            document.querySelectorAll('#modal-settings .provider-item input').forEach(cb => {
                cb.checked = (cfg.auto_import.providers || []).includes(cb.value);
            });

            updateAutoImportStatus(cfg.auto_import.enabled, cfg.auto_import.watch_path);
        } catch(e) {
            toast('加载配置失败: ' + e.message, 'error');
        }
    }

    function updateAutoImportStatus(enabled, watchPath) {
        const statusEl = $('auto-import-status');
        if (!statusEl) return;
        const dot = statusEl.querySelector('.status-dot');
        const label = statusEl.querySelector('.status-label');

        if (enabled) {
            if (dot) dot.classList.add('active');
            if (label) label.textContent = '运行中: ' + watchPath;
            $('btn-start-auto-import').style.display = 'none';
            $('btn-stop-auto-import').style.display = 'inline-flex';
        } else {
            if (dot) dot.classList.remove('active');
            if (label) label.textContent = '未启动';
            $('btn-start-auto-import').style.display = 'inline-flex';
            $('btn-stop-auto-import').style.display = 'none';
        }
    }

    async function saveSettings() {
        const providers = [];
        document.querySelectorAll('#modal-settings .provider-item input:checked').forEach(cb => {
            providers.push(cb.value);
        });

        try {
            await api('PUT', '/config', {
                auto_import: {
                    enabled: $('auto-import-enabled').checked,
                    watch_path: $('auto-import-path').value,
                    concurrency: parseInt($('auto-import-concurrency').value) || 4,
                    auto_tag: true,
                    providers: providers,
                    mode: $('auto-import-mode').value,
                    overwrite: $('auto-import-overwrite').checked,
                },
                default_settings: {
                    concurrency: parseInt($('auto-import-concurrency').value) || 4,
                    providers: providers,
                    mode: $('auto-import-mode').value,
                    overwrite: $('auto-import-overwrite').checked,
                    save_cover: false,
                    save_lyrics: false,
                },
                watch_folders: [],
            });
            toast('设置已保存', 'success');
        } catch(e) {
            toast('保存失败: ' + e.message, 'error');
        }
    }

    async function startAutoImport() {
        const providers = [];
        document.querySelectorAll('#modal-settings .provider-item input:checked').forEach(cb => {
            providers.push(cb.value);
        });

        const watchPath = $('auto-import-path').value;
        if (!watchPath) { toast('请输入监控路径', 'error'); return; }

        try {
            await api('POST', '/auto-import/start', {
                watch_path: watchPath,
                concurrency: parseInt($('auto-import-concurrency').value) || 4,
                auto_tag: true,
                providers: providers,
                mode: $('auto-import-mode').value,
                overwrite: $('auto-import-overwrite').checked,
            });
            toast('自动导入已启动', 'success');
            updateAutoImportStatus(true, watchPath);
            await saveSettings();
        } catch(e) {
            toast('启动失败: ' + e.message, 'error');
        }
    }

    async function stopAutoImport() {
        try {
            await api('POST', '/auto-import/stop', {});
            toast('自动导入已停止', 'success');
            updateAutoImportStatus(false, '');
        } catch(e) {
            toast('停止失败: ' + e.message, 'error');
        }
    }

    function formatSize(bytes) {
        if (!bytes) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB'];
        let i = 0, size = bytes;
        while (size >= 1024 && i < units.length - 1) { size /= 1024; i++; }
        return size.toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
    }

    function formatDuration(seconds) {
        if (!seconds) return '0:00';
        const m = Math.floor(seconds / 60);
        const s = Math.floor(seconds % 60);
        return m + ':' + s.toString().padStart(2, '0');
    }

    function escapeHtml(str) {
        if (!str) return '';
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    function escapeAttr(str) {
        if (!str) return '';
        return str.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/'/g, '&#39;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    window.openAutoTagModal = openAutoTagModal;
    window.openBatchRenameModal = openBatchRenameModal;
    window.$ = $;

    document.addEventListener('DOMContentLoaded', init);
})();
