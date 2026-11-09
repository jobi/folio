// ============================================================================
// FOLIO — Self-Hosted EPUB Library, Web Reader, KOReader Sync & CrossPoint UI
// ============================================================================

const state = {
  books: [],
  allBooksSnapshot: [],
  systemInfo: null,
  devices: [],
  selectedDeviceId: null,
  filterStatus: 'all',
  filterSeries: '',
  filterTag: '',
  searchQuery: '',
  sortBy: 'recent',
  viewMode: 'grid', // 'grid' | 'table'
  activeBook: null,

  // Web Reader State
  reader: {
    book: null,
    manifest: null,
    chapterIndex: 0,
    pageIndex: 0,
    totalPages: 1,
    pageWidth: 0,
    pageGap: 40,
    mode: localStorage.getItem('folio_reader_mode') || 'paginated', // 'paginated' | 'scroll'
    bookmarks: [],
    fontSize: parseInt(localStorage.getItem('folio_reader_font_size'), 10) || 19,
    fontFamily: localStorage.getItem('folio_reader_font_family') || 'serif',
    theme: localStorage.getItem('folio_reader_theme') || 'paper',
    measure: localStorage.getItem('folio_reader_measure') || '65ch',
    syncDebounceTimer: null,
  },
};

// --- Initialization ---

document.addEventListener('DOMContentLoaded', async () => {
  initTheme();
  bindNavigationEvents();
  bindMetadataDrawerEvents();
  bindReaderEvents();
  bindX3ModalEvents();
  bindSyncModalEvents();
  bindTailscaleModalEvents();
  bindDragAndDropUpload();
  bindCopyButtons();

  await refreshAll();
});

async function refreshAll() {
  await Promise.all([loadSystemInfo(), loadBooks(), loadDevices()]);
}

function showToast(message) {
  const container = document.getElementById('toast-container');
  const el = document.createElement('div');
  el.className = 'toast';
  el.textContent = message;
  container.appendChild(el);
  setTimeout(() => {
    el.style.opacity = '0';
    setTimeout(() => el.remove(), 220);
  }, 3600);
}

// --- Theme Handling ---

function initTheme() {
  const saved = localStorage.getItem('folio_theme') || 'light';
  document.documentElement.setAttribute('data-theme', saved);
  document.getElementById('btn-theme-toggle').addEventListener('click', () => {
    const current = document.documentElement.getAttribute('data-theme') || 'light';
    const next = current === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('folio_theme', next);
  });
}

// --- System & Library Fetching ---

async function loadSystemInfo() {
  try {
    const res = await fetch('/api/system/info');
    if (!res.ok) return;
    const info = await res.json();
    state.systemInfo = info;

    document.getElementById('sys-db-driver').textContent =
      info.db_driver === 'postgres' ? 'PostgreSQL' : 'SQLite';

    document.getElementById('hub-opds-v1').textContent = info.opds_v1_url;
    document.getElementById('hub-opds-v2').textContent = info.opds_v2_url;
    document.getElementById('hub-kosync-url').textContent = info.kosync_url;

    if (info.tailscale) {
      renderTailscaleSession(info.tailscale);
    }

    renderSidebarFacets(info.series_counts || {}, info.tag_counts || {});
    renderSyncActivity(info.recent_progress || []);
  } catch (err) {
    console.error('Failed to load system info:', err);
  }
}

async function loadBooks() {
  try {
    const params = new URLSearchParams();
    if (state.searchQuery) params.set('q', state.searchQuery);
    if (state.filterTag) params.set('tag', state.filterTag);
    if (state.filterSeries) params.set('series', state.filterSeries);
    if (state.filterStatus && state.filterStatus !== 'all') {
      params.set('status', state.filterStatus);
    }
    params.set('sort', state.sortBy);

    const res = await fetch(`/api/books?${params.toString()}`);
    const data = await res.json();
    state.books = data.books || [];

    // Also keep an unfiltered snapshot for sidebar badge counts
    if (!state.searchQuery && !state.filterTag && !state.filterSeries && state.filterStatus === 'all') {
      state.allBooksSnapshot = state.books;
      updateCollectionCounts(state.allBooksSnapshot);
    } else {
      const allRes = await fetch('/api/books');
      const allData = await allRes.json();
      state.allBooksSnapshot = allData.books || [];
      updateCollectionCounts(state.allBooksSnapshot);
    }

    renderBooks();
    populateX3BookSelect();
  } catch (err) {
    console.error('Failed to load books:', err);
  }
}

function updateCollectionCounts(allBooks) {
  document.getElementById('count-all').textContent = allBooks.length;
  document.getElementById('count-reading').textContent = allBooks.filter(
    (b) => b.reading_status === 'reading'
  ).length;
  document.getElementById('count-unread').textContent = allBooks.filter(
    (b) => !b.reading_status || b.reading_status === 'unread'
  ).length;
  document.getElementById('count-finished').textContent = allBooks.filter(
    (b) => b.reading_status === 'finished'
  ).length;
}

function renderSidebarFacets(seriesCounts, tagCounts) {
  const container = document.getElementById('sidebar-facets');
  container.innerHTML = '';

  Object.entries(seriesCounts)
    .sort((a, b) => a[0].localeCompare(b[0]))
    .forEach(([seriesName, count]) => {
      const btn = document.createElement('button');
      btn.className = `nav-item ${state.filterSeries === seriesName ? 'active' : ''}`;
      btn.innerHTML = `
        <span class="nav-item-left" title="Series: ${escapeHTML(seriesName)}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1 0-5H20"/></svg>
          ${escapeHTML(seriesName)}
        </span>
        <span class="nav-badge">${count}</span>
      `;
      btn.addEventListener('click', () => {
        state.filterSeries = state.filterSeries === seriesName ? '' : seriesName;
        state.filterTag = '';
        updateFilterHeader();
        loadBooks();
        renderSidebarFacets(seriesCounts, tagCounts);
      });
      container.appendChild(btn);
    });

  Object.entries(tagCounts)
    .sort((a, b) => a[0].localeCompare(b[0]))
    .slice(0, 12)
    .forEach(([tagName, count]) => {
      const btn = document.createElement('button');
      btn.className = `nav-item ${state.filterTag === tagName ? 'active' : ''}`;
      btn.innerHTML = `
        <span class="nav-item-left" title="Tag: ${escapeHTML(tagName)}">
          <span style="color:var(--accent-primary)">#</span>
          ${escapeHTML(tagName)}
        </span>
        <span class="nav-badge">${count}</span>
      `;
      btn.addEventListener('click', () => {
        state.filterTag = state.filterTag === tagName ? '' : tagName;
        state.filterSeries = '';
        updateFilterHeader();
        loadBooks();
        renderSidebarFacets(seriesCounts, tagCounts);
      });
      container.appendChild(btn);
    });
}

function updateFilterHeader() {
  const titleMap = {
    all: 'All Volumes',
    reading: 'Currently Reading',
    unread: 'To Read',
    finished: 'Completed Volumes',
  };
  document.getElementById('catalog-view-title').textContent =
    titleMap[state.filterStatus] || 'All Volumes';

  const pill = document.getElementById('active-filter-pill');
  if (state.filterSeries) {
    pill.textContent = `Series: ${state.filterSeries} ×`;
    pill.classList.remove('hidden');
  } else if (state.filterTag) {
    pill.textContent = `#${state.filterTag} ×`;
    pill.classList.remove('hidden');
  } else {
    pill.classList.add('hidden');
  }
}

// --- Render Library Grid & Table ---

function renderBooks() {
  const grid = document.getElementById('books-grid');
  const tableWrap = document.getElementById('books-table-wrap');
  const tableBody = document.getElementById('books-table-body');

  if (state.viewMode === 'grid') {
    grid.classList.remove('hidden');
    tableWrap.classList.add('hidden');
  } else {
    grid.classList.add('hidden');
    tableWrap.classList.remove('hidden');
  }

  grid.innerHTML = '';
  tableBody.innerHTML = '';

  if (state.books.length === 0) {
    grid.innerHTML = `
      <div style="grid-column: 1 / -1; text-align:center; padding: 64px 20px; color: var(--text-muted);">
        <h3 style="font-family:var(--font-serif); font-size:22px; color:var(--text-primary); margin-bottom:6px;">No matching EPUB volumes</h3>
        <p>Import an <code>.epub</code> file or clear your active search filters.</p>
      </div>
    `;
    return;
  }

  state.books.forEach((book) => {
    const pct = book.latest_progress ? Math.round(book.latest_progress.percentage * 100) : 0;
    const deviceLabel = book.latest_progress ? book.latest_progress.device : '';

    // 1. Grid Card
    const card = document.createElement('article');
    card.className = 'book-card';
    card.innerHTML = `
      <div class="book-cover-stage" data-book-id="${book.id}">
        <img src="${book.cover_url}" alt="${escapeHTML(book.title)}" class="book-cover-img" loading="lazy" />
        ${
          pct > 0
            ? `<div class="cover-progress-ribbon"><div class="cover-progress-fill" style="width:${pct}%"></div></div>`
            : ''
        }
        <div class="book-card-actions">
          <button type="button" class="card-action-pill" data-action="read" data-id="${book.id}">Read</button>
          <button type="button" class="card-action-pill" data-action="push" data-id="${book.id}" title="Push to CrossPoint Reader">Push</button>
          <button type="button" class="card-action-pill" data-action="edit" data-id="${book.id}">Edit</button>
        </div>
      </div>
      <div class="book-meta-foot">
        ${
          book.series
            ? `<div class="book-series-tag">${escapeHTML(book.series)}${
                book.series_index ? ' #' + book.series_index : ''
              }</div>`
            : ''
        }
        <div class="book-title-link" data-action="read" data-id="${book.id}">${escapeHTML(book.title)}</div>
        <div class="book-author-line">${escapeHTML(book.authors)}</div>
        <div class="book-sync-pill-row">
          <span>${pct > 0 ? `${pct}% read` : 'Unread'}</span>
          ${deviceLabel ? `<span>${escapeHTML(deviceLabel)}</span>` : `<span>${book.page_count}p</span>`}
        </div>
      </div>
    `;

    card.querySelectorAll('[data-action]').forEach((el) => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        const act = el.getAttribute('data-action');
        if (act === 'read') openWebReader(book.id);
        if (act === 'push') openX3ModalForBook(book.id);
        if (act === 'edit') openMetadataDrawer(book.id);
      });
    });
    card.querySelector('.book-cover-stage').addEventListener('click', () => {
      openMetadataDrawer(book.id);
    });

    grid.appendChild(card);

    // 2. Table Row
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td>
        <div style="font-family:var(--font-serif); font-weight:600; font-size:15px; cursor:pointer;" class="tbl-read-link">${escapeHTML(
          book.title
        )}</div>
        ${
          book.series
            ? `<div style="font-size:11.5px; color:var(--accent-primary);">${escapeHTML(book.series)} #${
                book.series_index || 1
              }</div>`
            : ''
        }
      </td>
      <td>${escapeHTML(book.authors)}</td>
      <td>${escapeHTML(book.tags || '—')}</td>
      <td class="mono-val">${pct}% ${deviceLabel ? `(${escapeHTML(deviceLabel)})` : ''}</td>
      <td><code class="mono-pill">${escapeHTML((book.ko_md5_partial || '').slice(0, 12))}…</code></td>
      <td class="text-right">
        <button type="button" class="btn btn-xs btn-primary tbl-btn-read">Read</button>
        <button type="button" class="btn btn-xs btn-secondary tbl-btn-push" title="Push to CrossPoint Reader">Push</button>
        <button type="button" class="btn btn-xs btn-ghost tbl-btn-edit">Metadata</button>
      </td>
    `;
    tr.querySelector('.tbl-read-link').addEventListener('click', () => openWebReader(book.id));
    tr.querySelector('.tbl-btn-read').addEventListener('click', () => openWebReader(book.id));
    tr.querySelector('.tbl-btn-push').addEventListener('click', () => openX3ModalForBook(book.id));
    tr.querySelector('.tbl-btn-edit').addEventListener('click', () => openMetadataDrawer(book.id));
    tableBody.appendChild(tr);
  });
}

// --- Navigation & Upload Events ---

function bindNavigationEvents() {
  document.querySelectorAll('[data-status]').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('[data-status]').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      state.filterStatus = btn.getAttribute('data-status');
      updateFilterHeader();
      loadBooks();
    });
  });

  document.getElementById('active-filter-pill').addEventListener('click', () => {
    state.filterSeries = '';
    state.filterTag = '';
    updateFilterHeader();
    loadBooks();
    if (state.systemInfo) {
      renderSidebarFacets(state.systemInfo.series_counts || {}, state.systemInfo.tag_counts || {});
    }
  });

  const searchInput = document.getElementById('search-input');
  const clearBtn = document.getElementById('btn-clear-search');
  let searchTimer = null;

  searchInput.addEventListener('input', () => {
    state.searchQuery = searchInput.value.trim();
    clearBtn.classList.toggle('hidden', !state.searchQuery);
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => loadBooks(), 180);
  });

  clearBtn.addEventListener('click', () => {
    searchInput.value = '';
    state.searchQuery = '';
    clearBtn.classList.add('hidden');
    loadBooks();
  });

  // Keyboard shortcut '/' or Cmd+K to focus search
  document.addEventListener('keydown', (e) => {
    if ((e.key === '/' || (e.key === 'k' && (e.metaKey || e.ctrlKey))) && !isInputFocused()) {
      e.preventDefault();
      searchInput.focus();
    }
  });

  document.getElementById('sort-select').addEventListener('change', (e) => {
    state.sortBy = e.target.value;
    loadBooks();
  });

  document.getElementById('btn-view-grid').addEventListener('click', () => {
    state.viewMode = 'grid';
    document.getElementById('btn-view-grid').classList.add('active');
    document.getElementById('btn-view-table').classList.remove('active');
    renderBooks();
  });

  document.getElementById('btn-view-table').addEventListener('click', () => {
    state.viewMode = 'table';
    document.getElementById('btn-view-table').classList.add('active');
    document.getElementById('btn-view-grid').classList.remove('active');
    renderBooks();
  });

  const fileInput = document.getElementById('epub-file-input');
  fileInput.multiple = true;
  fileInput.setAttribute('multiple', 'multiple');

  document.getElementById('btn-upload-epub').addEventListener('click', () => {
    fileInput.multiple = true;
    fileInput.setAttribute('multiple', 'multiple');
    fileInput.click();
  });

  fileInput.addEventListener('change', async () => {
    if (fileInput.files && fileInput.files.length > 0) {
      const selectedFiles = Array.from(fileInput.files);
      fileInput.value = '';
      await uploadEPUBFiles(selectedFiles);
    }
  });
}

async function uploadEPUBFiles(fileList) {
  const files = Array.from(fileList || []).filter((f) => f && f.name);
  if (files.length === 0) return;

  const btn = document.getElementById('btn-upload-epub');
  const labelEl = document.getElementById('btn-upload-epub-label');
  const origLabel = labelEl ? labelEl.textContent : 'Import EPUBs';
  if (btn) btn.disabled = true;

  showToast(`Importing ${files.length} EPUB volume(s)...`);

  let totalImported = 0;
  const allErrors = [];

  try {
    for (let i = 0; i < files.length; i++) {
      if (labelEl) {
        labelEl.textContent = files.length > 1 ? `Importing ${i + 1}/${files.length}...` : 'Importing...';
      }
      const formData = new FormData();
      formData.append('files', files[i]);
      const res = await fetch('/api/books/upload', {
        method: 'POST',
        body: formData,
      });
      const data = await res.json();
      if (!res.ok) {
        allErrors.push(`${files[i].name}: ${data.error || 'Upload failed'}`);
        continue;
      }
      totalImported += (data.imported || []).length;
      if (Array.isArray(data.errors) && data.errors.length > 0) {
        allErrors.push(...data.errors);
      }
    }

    if (totalImported > 0) {
      showToast(`Imported ${totalImported} EPUB book(s) with cover & KOReader MD5 hashes!`);
    }
    if (allErrors.length > 0) {
      showToast(`Note: ${allErrors.join('; ')}`);
    }
    await refreshAll();
  } catch (err) {
    showToast(`Upload failed: ${err.message}`);
  } finally {
    if (btn) btn.disabled = false;
    if (labelEl) labelEl.textContent = origLabel;
  }
}

async function collectDroppedEPUBFiles(dataTransfer) {
  if (!dataTransfer) return [];
  if (dataTransfer.items && dataTransfer.items.length > 0) {
    const entries = [];
    for (const item of Array.from(dataTransfer.items)) {
      if (item.kind === 'file' && typeof item.webkitGetAsEntry === 'function') {
        const entry = item.webkitGetAsEntry();
        if (entry) entries.push(entry);
      }
    }
    if (entries.length > 0) {
      const collected = [];
      const readEntry = (entry) =>
        new Promise((resolve) => {
          if (entry.isFile) {
            entry.file(
              (file) => {
                if (file.name.toLowerCase().endsWith('.epub')) {
                  collected.push(file);
                }
                resolve();
              },
              () => resolve()
            );
          } else if (entry.isDirectory) {
            const reader = entry.createReader();
            const readBatch = () => {
              reader.readEntries(
                async (batch) => {
                  if (!batch || batch.length === 0) {
                    resolve();
                    return;
                  }
                  await Promise.all(batch.map(readEntry));
                  readBatch();
                },
                () => resolve()
              );
            };
            readBatch();
          } else {
            resolve();
          }
        });
      await Promise.all(entries.map(readEntry));
      if (collected.length > 0) return collected;
    }
  }
  return Array.from(dataTransfer.files || []);
}

function bindDragAndDropUpload() {
  const overlay = document.getElementById('dropzone-overlay');
  let dragDepth = 0;

  window.addEventListener('dragenter', (e) => {
    if (e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files')) {
      dragDepth++;
      overlay.classList.remove('hidden');
    }
  });

  window.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) overlay.classList.add('hidden');
  });

  window.addEventListener('dragover', (e) => e.preventDefault());

  window.addEventListener('drop', async (e) => {
    e.preventDefault();
    dragDepth = 0;
    overlay.classList.add('hidden');
    const files = await collectDroppedEPUBFiles(e.dataTransfer);
    if (files.length > 0) {
      await uploadEPUBFiles(files);
    }
  });
}

// --- Metadata & Cover Inspector Drawer ---

async function openMetadataDrawer(bookId) {
  try {
    const res = await fetch(`/api/books/${bookId}`);
    if (!res.ok) return;
    const data = await res.json();
    const book = data.book;
    state.activeBook = book;

    document.getElementById('meta-book-id').value = book.id;
    document.getElementById('drawer-book-heading').textContent = book.title;
    document.getElementById('meta-cover-preview').src = book.cover_url;
    document.getElementById('btn-drawer-download').href = `/api/books/${book.id}/download/${encodeURIComponent(
      book.file_name
    )}`;

    document.getElementById('meta-title').value = book.title || '';
    document.getElementById('meta-subtitle').value = book.subtitle || '';
    document.getElementById('meta-authors').value = book.authors || '';
    document.getElementById('meta-status').value = book.reading_status || 'unread';
    document.getElementById('meta-series').value = book.series || '';
    document.getElementById('meta-series-index').value = book.series_index || '';
    document.getElementById('meta-tags').value = book.tags || '';
    document.getElementById('meta-description').value = book.description || '';
    document.getElementById('meta-publisher').value = book.publisher || '';
    document.getElementById('meta-published-date').value = book.published_date || '';
    document.getElementById('meta-language').value = book.language || 'en';
    document.getElementById('meta-isbn').value = book.isbn || '';
    document.getElementById('meta-filename').value = book.file_name || '';

    document.getElementById('meta-hash-partial').textContent = book.ko_md5_partial || '—';
    document.getElementById('meta-hash-filename').textContent = book.ko_md5_filename || '—';

    if (book.latest_progress) {
      const pct = (book.latest_progress.percentage * 100).toFixed(1);
      document.getElementById(
        'meta-progress-text'
      ).textContent = `${pct}% on ${book.latest_progress.device} (${book.latest_progress.progress})`;
      document.getElementById('meta-progress-bar').style.width = `${pct}%`;
    } else {
      document.getElementById('meta-progress-text').textContent = '0% — No progress synced yet';
      document.getElementById('meta-progress-bar').style.width = '0%';
    }

    document.getElementById('metadata-backdrop').classList.remove('hidden');
    document.getElementById('metadata-drawer').classList.remove('hidden');
  } catch (err) {
    console.error('Failed to open metadata drawer:', err);
  }
}

function closeMetadataDrawer() {
  document.getElementById('metadata-backdrop').classList.add('hidden');
  document.getElementById('metadata-drawer').classList.add('hidden');
}

function bindMetadataDrawerEvents() {
  document.getElementById('btn-close-metadata').addEventListener('click', closeMetadataDrawer);
  document.getElementById('metadata-backdrop').addEventListener('click', closeMetadataDrawer);

  document.getElementById('btn-drawer-read').addEventListener('click', () => {
    if (state.activeBook) {
      closeMetadataDrawer();
      openWebReader(state.activeBook.id);
    }
  });

  document.getElementById('btn-drawer-push-x3').addEventListener('click', () => {
    if (state.activeBook) {
      closeMetadataDrawer();
      openX3ModalForBook(state.activeBook.id);
    }
  });

  // Upload custom cover
  const coverInput = document.getElementById('cover-file-input');
  document.getElementById('btn-change-cover').addEventListener('click', () => coverInput.click());
  coverInput.addEventListener('change', async () => {
    if (!coverInput.files || !coverInput.files[0] || !state.activeBook) return;
    const fd = new FormData();
    fd.append('cover', coverInput.files[0]);
    const res = await fetch(`/api/books/${state.activeBook.id}/cover`, {
      method: 'POST',
      body: fd,
    });
    if (res.ok) {
      const data = await res.json();
      state.activeBook = data.book;
      document.getElementById('meta-cover-preview').src = `${data.book.cover_url}&t=${Date.now()}`;
      document.getElementById('meta-hash-partial').textContent = data.book.ko_md5_partial;
      showToast('Updated book cover and embedded into EPUB archive!');
      await loadBooks();
    }
    coverInput.value = '';
  });

  // Auto-generate bookplate cover
  document.getElementById('btn-regen-cover').addEventListener('click', async () => {
    if (!state.activeBook) return;
    const res = await fetch(`/api/books/${state.activeBook.id}/cover`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ regenerate: true }),
    });
    if (res.ok) {
      const data = await res.json();
      state.activeBook = data.book;
      document.getElementById('meta-cover-preview').src = `${data.book.cover_url}&t=${Date.now()}`;
      document.getElementById('meta-hash-partial').textContent = data.book.ko_md5_partial;
      showToast('Generated bespoke bookplate cover!');
      await loadBooks();
    }
  });

  // Save metadata form
  document.getElementById('metadata-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const bookId = document.getElementById('meta-book-id').value;
    const payload = {
      title: document.getElementById('meta-title').value,
      subtitle: document.getElementById('meta-subtitle').value,
      authors: document.getElementById('meta-authors').value,
      reading_status: document.getElementById('meta-status').value,
      series: document.getElementById('meta-series').value,
      series_index: parseFloat(document.getElementById('meta-series-index').value) || 0,
      tags: document.getElementById('meta-tags').value,
      description: document.getElementById('meta-description').value,
      publisher: document.getElementById('meta-publisher').value,
      published_date: document.getElementById('meta-published-date').value,
      language: document.getElementById('meta-language').value,
      isbn: document.getElementById('meta-isbn').value,
      file_name: document.getElementById('meta-filename').value,
      repack_epub: document.getElementById('meta-repack-epub').checked,
    };

    const res = await fetch(`/api/books/${bookId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (res.ok) {
      const data = await res.json();
      state.activeBook = data.book;
      document.getElementById('meta-hash-partial').textContent = data.book.ko_md5_partial;
      document.getElementById('meta-hash-filename').textContent = data.book.ko_md5_filename;
      showToast('Saved metadata and updated EPUB OPF package!');
      await refreshAll();
      closeMetadataDrawer();
    } else {
      const err = await res.json();
      showToast(`Error saving metadata: ${err.error || 'Failed'}`);
    }
  });

  // Delete book
  document.getElementById('btn-delete-book').addEventListener('click', async () => {
    if (!state.activeBook) return;
    const res = await fetch(`/api/books/${state.activeBook.id}`, { method: 'DELETE' });
    if (res.ok) {
      showToast(`Removed "${state.activeBook.title}" from library.`);
      closeMetadataDrawer();
      await refreshAll();
    }
  });
}

// --- Built-in Web EPUB Reader & Live KOReader Sync ---

async function openWebReader(bookId) {
  try {
    const [bookRes, manifestRes] = await Promise.all([
      fetch(`/api/books/${bookId}`),
      fetch(`/api/books/${bookId}/manifest`),
    ]);
    if (!bookRes.ok || !manifestRes.ok) {
      showToast('Failed to load EPUB manifest.');
      return;
    }
    const bookData = await bookRes.json();
    const manifest = await manifestRes.json();

    state.reader.book = bookData.book;
    state.reader.manifest = manifest;
    state.reader.bookmarks = bookData.bookmarks || [];

    document.getElementById('reader-book-title').textContent = bookData.book.title;
    document.getElementById('web-reader-overlay').classList.remove('hidden');

    initReaderPreferences();
    renderReaderTOC();
    renderReaderBookmarks();

    // Check KOReader sync progress to resume at exact chapter & page
    let targetSpine = 0;
    let targetRatio = 0;
    const lp = bookData.book.latest_progress;
    if (lp) {
      if (lp.position && typeof lp.position.spine === 'number') {
        targetSpine = lp.position.spine;
      } else if (lp.progress) {
        const fragMatch = lp.progress.match(/DocFragment\[(\d+)\]/i);
        if (fragMatch) {
          targetSpine = Math.max(0, parseInt(fragMatch[1], 10) - 1);
        } else {
          const foundIdx = manifest.chapters.findIndex(
            (ch) => lp.percentage >= ch.start_pct && lp.percentage <= ch.end_pct
          );
          if (foundIdx >= 0) targetSpine = foundIdx;
        }
      }
      const ch = manifest.chapters[targetSpine];
      if (ch && ch.end_pct > ch.start_pct) {
        targetRatio = Math.max(
          0,
          Math.min(1, (lp.percentage - ch.start_pct) / (ch.end_pct - ch.start_pct))
        );
      }
    }

    await loadReaderChapter(targetSpine, targetRatio);
  } catch (err) {
    console.error('Reader open error:', err);
    showToast('Error opening Web Reader.');
  }
}

async function loadReaderChapter(spineIndex, scrollRatio = 0, targetPageIndex = null) {
  const { book, manifest } = state.reader;
  if (!book || !manifest) return;

  spineIndex = Math.max(0, Math.min(manifest.chapters.length - 1, spineIndex));
  state.reader.chapterIndex = spineIndex;

  const res = await fetch(`/api/books/${book.id}/chapter/${spineIndex}`);
  if (!res.ok) return;
  const chData = await res.json();

  document.getElementById('reader-chapter-subtitle').textContent = chData.title || `Chapter ${spineIndex + 1}`;
  document.getElementById('reader-chapter-custom-css').textContent = chData.styles || '';
  const article = document.getElementById('reader-article');
  article.innerHTML = chData.html;

  // Bind internal EPUB links
  article.querySelectorAll('a[data-spine-index]').forEach((a) => {
    a.addEventListener('click', (e) => {
      e.preventDefault();
      const idx = parseInt(a.getAttribute('data-spine-index'), 10);
      loadReaderChapter(idx, 0, 0);
    });
  });

  document.getElementById('btn-prev-chapter').disabled = spineIndex <= 0;
  document.getElementById('btn-next-chapter').disabled = spineIndex >= manifest.chapters.length - 1;

  // Highlight active TOC item
  document.querySelectorAll('#reader-toc-list .toc-entry-btn').forEach((btn) => {
    btn.classList.toggle('active', parseInt(btn.getAttribute('data-spine'), 10) === spineIndex);
  });

  if (state.reader.mode === 'paginated') {
    setTimeout(() => {
      updateReaderPaginationGeometry();
      if (targetPageIndex === -1) {
        goToPage(state.reader.totalPages - 1, false);
      } else if (typeof targetPageIndex === 'number') {
        goToPage(targetPageIndex, false);
      } else if (scrollRatio > 0 && state.reader.totalPages > 1) {
        const targetPage = Math.min(state.reader.totalPages - 1, Math.round(scrollRatio * (state.reader.totalPages - 1)));
        goToPage(targetPage, false);
      } else {
        goToPage(0, false);
      }
    }, 40);
  } else {
    const scroller = document.getElementById('reader-scroll-container');
    setTimeout(() => {
      const maxScroll = Math.max(0, scroller.scrollHeight - scroller.clientHeight);
      scroller.scrollTop = Math.round(maxScroll * scrollRatio);
      updateReaderProgressMetrics(true);
    }, 40);
  }
}

function updateReaderPaginationGeometry() {
  if (state.reader.mode !== 'paginated') return;
  const viewport = document.getElementById('reader-page-viewport');
  const slider = document.getElementById('reader-columns-slider');
  const article = document.getElementById('reader-article');
  if (!viewport || !slider || !article) return;

  const w = slider.clientWidth;
  if (w <= 0) return;
  const gap = w < 600 ? 24 : 40;

  article.style.setProperty('--reader-col-width', `${w}px`);
  article.style.setProperty('--reader-col-gap', `${gap}px`);

  // Calculate total pages from columns
  const sw = article.scrollWidth;
  let total = 1;
  if (sw > w + 2) {
    total = Math.max(1, Math.ceil((sw - w) / (w + gap)) + 1);
  }
  state.reader.totalPages = total;
  state.reader.pageWidth = w;
  state.reader.pageGap = gap;
  state.reader.pageIndex = Math.max(0, Math.min(total - 1, state.reader.pageIndex));
}

function goToPage(idx, animate = true) {
  const { totalPages, pageWidth, pageGap, chapterIndex, manifest } = state.reader;
  idx = Math.max(0, Math.min((totalPages || 1) - 1, idx));
  state.reader.pageIndex = idx;

  const slider = document.getElementById('reader-columns-slider');
  if (slider && state.reader.mode === 'paginated') {
    slider.style.transition = animate ? 'transform 0.26s cubic-bezier(0.2, 0, 0.2, 1)' : 'none';
    const stride = (pageWidth || slider.clientWidth) + (pageGap || 40);
    slider.style.transform = `translateX(-${idx * stride}px)`;
  }

  // Update floating page turn arrow states
  const prevBtn = document.getElementById('btn-reader-prev-page');
  const nextBtn = document.getElementById('btn-reader-next-page');
  const atStart = chapterIndex <= 0 && idx <= 0;
  const atEnd = chapterIndex >= ((manifest && manifest.chapters.length) || 1) - 1 && idx >= (totalPages - 1);
  if (prevBtn) prevBtn.disabled = atStart;
  if (nextBtn) nextBtn.disabled = atEnd;

  updateReaderProgressMetrics(true);
}

function nextPage() {
  if (state.reader.mode !== 'paginated') return;
  const { pageIndex, totalPages, chapterIndex, manifest } = state.reader;
  if (pageIndex < totalPages - 1) {
    goToPage(pageIndex + 1, true);
  } else if (manifest && chapterIndex < manifest.chapters.length - 1) {
    loadReaderChapter(chapterIndex + 1, 0, 0);
  } else {
    showToast("You've reached the end of the book!");
  }
}

function prevPage() {
  if (state.reader.mode !== 'paginated') return;
  const { pageIndex, chapterIndex } = state.reader;
  if (pageIndex > 0) {
    goToPage(pageIndex - 1, true);
  } else if (chapterIndex > 0) {
    loadReaderChapter(chapterIndex - 1, 1, -1);
  }
}

function toggleReaderChrome() {
  const overlay = document.getElementById('web-reader-overlay');
  overlay.classList.toggle('chrome-hidden');
  document.getElementById('reader-typo-popover').classList.add('hidden');
  document.getElementById('reader-toc-sidebar').classList.add('hidden');
}

function setReaderMode(mode) {
  state.reader.mode = mode;
  localStorage.setItem('folio_reader_mode', mode);

  const container = document.getElementById('reader-scroll-container');
  if (!container) return;

  if (mode === 'paginated') {
    container.classList.remove('mode-scroll');
    container.classList.add('mode-paginated');
  } else {
    container.classList.remove('mode-paginated');
    container.classList.add('mode-scroll');
  }

  document.querySelectorAll('[data-reader-mode]').forEach((btn) => {
    btn.classList.toggle('active', btn.getAttribute('data-reader-mode') === mode);
  });

  applyReaderTypography();
  if (mode === 'paginated') {
    setTimeout(() => {
      updateReaderPaginationGeometry();
      goToPage(state.reader.pageIndex, false);
    }, 40);
  } else {
    const slider = document.getElementById('reader-columns-slider');
    if (slider) slider.style.transform = '';
    const article = document.getElementById('reader-article');
    if (article) {
      article.style.removeProperty('--reader-col-width');
      article.style.removeProperty('--reader-col-gap');
    }
    updateReaderProgressMetrics(false);
  }
}

function seekToGlobalPercentage(targetPct) {
  const { manifest } = state.reader;
  if (!manifest || !manifest.chapters.length) return;
  targetPct = Math.max(0, Math.min(1, targetPct));

  const targetSpine = manifest.chapters.findIndex(
    (ch) => targetPct >= ch.start_pct && targetPct <= ch.end_pct
  );
  const spineIdx = targetSpine >= 0 ? targetSpine : (targetPct >= 1 ? manifest.chapters.length - 1 : 0);
  const ch = manifest.chapters[spineIdx];
  const ratio = (ch.end_pct > ch.start_pct) ? Math.max(0, Math.min(1, (targetPct - ch.start_pct) / (ch.end_pct - ch.start_pct))) : 0;

  loadReaderChapter(spineIdx, ratio);
}

function updateReaderProgressMetrics(syncToServer = false) {
  const { book, manifest, chapterIndex, totalPages, pageIndex, mode } = state.reader;
  if (!book || !manifest || !manifest.chapters.length) return;

  const ch = manifest.chapters[chapterIndex];
  let chapterRatio = 0;
  if (mode === 'paginated') {
    chapterRatio = totalPages > 1 ? (pageIndex / (totalPages - 1)) : 0;
  } else {
    const scroller = document.getElementById('reader-scroll-container');
    const maxScroll = Math.max(1, scroller.scrollHeight - scroller.clientHeight);
    chapterRatio = Math.max(0, Math.min(1, scroller.scrollTop / maxScroll));
  }

  const overallPct = ch.start_pct + chapterRatio * (ch.end_pct - ch.start_pct);
  const clampedPct = Math.max(0.001, Math.min(1, overallPct));

  // Estimate paragraph index inside chapter for KOReader XPath and CrossPoint CompactPosition
  const paragraphs = document.querySelectorAll('#reader-article p, #reader-article h2, #reader-article h3, #reader-article li');
  const totalParas = Math.max(1, paragraphs.length);
  const paraIndex = Math.max(1, Math.min(totalParas, Math.round(chapterRatio * (totalParas - 1)) + 1));
  const docFrag = chapterIndex + 1;
  const xpath = `/body/DocFragment[${docFrag}]/body/section[1]/p[${paraIndex}]/text().0`;

  const pctDisplay = `${(clampedPct * 100).toFixed(1)}%`;
  document.getElementById('reader-progress-fill').style.width = pctDisplay;
  document.getElementById('reader-percent-display').textContent = pctDisplay;

  const pageIndicator = document.getElementById('reader-page-indicator');
  if (pageIndicator) {
    if (mode === 'paginated') {
      pageIndicator.textContent = `Page ${pageIndex + 1} of ${totalPages || 1}`;
    } else {
      pageIndicator.textContent = `Scroll View`;
    }
  }

  const chFooterLabel = document.getElementById('reader-chapter-footer-label');
  if (chFooterLabel) {
    chFooterLabel.textContent = ch.title || `Chapter ${docFrag}`;
  }

  document.getElementById('reader-footer-pos').textContent = `Ch. ${docFrag}/${manifest.chapters.length} • ${pctDisplay}`;
  document.getElementById('reader-xpath-display').textContent = `KOSync XPath: ${xpath}`;

  if (syncToServer) {
    clearTimeout(state.reader.syncDebounceTimer);
    state.reader.syncDebounceTimer = setTimeout(() => {
      const activePageNum = mode === 'paginated' ? (pageIndex + 1) : paraIndex;
      const totalPageCount = mode === 'paginated' ? (totalPages || 1) : totalParas;
      pushReaderProgressToKOSync(clampedPct, xpath, chapterIndex, activePageNum, totalPageCount);
    }, 350);
  }
}

async function pushReaderProgressToKOSync(percentage, xpath, spineIdx, paraIdx, totalParas) {
  const { book } = state.reader;
  if (!book) return;

  const payload = {
    document: book.ko_md5_partial,
    book_id: book.id,
    progress: xpath,
    percentage: Number(percentage.toFixed(4)),
    device: 'Folio Web Reader',
    device_id: 'folio-web',
    position: {
      pctQ: Math.round(percentage * 1000000),
      spine: spineIdx,
      page: paraIdx,
      pages: Math.max(1, totalParas),
      para: paraIdx,
      xpath: xpath,
    },
    metadata: {
      filename: book.file_name,
      title: book.title,
      authors: book.authors,
      language: book.language,
    },
  };

  try {
    const res = await fetch('/syncs/progress', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Folio-Web': '1',
      },
      body: JSON.stringify(payload),
    });
    if (res.ok) {
      document.getElementById('reader-sync-text').textContent = `Synced ${(percentage * 100).toFixed(0)}%`;
    }
  } catch (err) {
    console.error('KOSync update failed:', err);
  }
}

function renderReaderTOC() {
  const list = document.getElementById('reader-toc-list');
  list.innerHTML = '';
  const { manifest } = state.reader;
  if (!manifest) return;

  (manifest.toc || []).forEach((item) => {
    const btn = document.createElement('button');
    btn.className = 'toc-entry-btn';
    btn.setAttribute('data-spine', item.spine_index);
    btn.textContent = item.title;
    btn.addEventListener('click', () => {
      loadReaderChapter(item.spine_index, 0, 0);
    });
    list.appendChild(btn);
  });
}

function renderReaderBookmarks() {
  const list = document.getElementById('reader-bookmarks-list');
  list.innerHTML = '';
  const bms = (state.reader.bookmarks || []).filter((b) => !b.deleted);
  if (bms.length === 0) {
    list.innerHTML = `<div style="padding:14px; color:var(--text-muted); font-size:12.5px;">No bookmarks yet. Click the bookmark icon in the reader bar to save one.</div>`;
    return;
  }

  bms.forEach((bm) => {
    const btn = document.createElement('button');
    btn.className = 'toc-entry-btn';
    const pct = (bm.percentage * 100).toFixed(1);
    btn.innerHTML = `
      <div style="font-weight:600; font-size:12.5px;">${escapeHTML(bm.summary || bm.xpath)}</div>
      <div class="mono-val" style="font-size:11px; color:var(--text-muted); margin-top:2px;">${pct}% &bull; ${escapeHTML(
      bm.xpath
    )}</div>
    `;
    btn.addEventListener('click', () => {
      loadReaderChapter(bm.si || 0, 0, 0);
    });
    list.appendChild(btn);
  });
}

function bindReaderTouchAndSwipe() {
  const container = document.getElementById('reader-scroll-container');
  if (!container) return;

  let startX = 0;
  let startY = 0;
  let startTime = 0;
  let isTouching = false;
  let isHorizontalMove = false;

  container.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1) return;
    startX = e.touches[0].clientX;
    startY = e.touches[0].clientY;
    startTime = Date.now();
    isTouching = true;
    isHorizontalMove = false;
  }, { passive: true });

  container.addEventListener('touchmove', (e) => {
    if (!isTouching || e.touches.length !== 1) return;
    if (state.reader.mode !== 'paginated') return;

    const curX = e.touches[0].clientX;
    const curY = e.touches[0].clientY;
    const dx = curX - startX;
    const dy = curY - startY;

    if (!isHorizontalMove) {
      if (Math.abs(dx) > 10 && Math.abs(dx) > Math.abs(dy)) {
        isHorizontalMove = true;
      } else if (Math.abs(dy) > 10) {
        isTouching = false;
        return;
      }
    }

    if (isHorizontalMove) {
      if (e.cancelable) e.preventDefault();
      const slider = document.getElementById('reader-columns-slider');
      if (slider) {
        const stride = (state.reader.pageWidth || slider.clientWidth) + (state.reader.pageGap || 40);
        const base = -state.reader.pageIndex * stride;
        const offset = dx * 0.35;
        slider.style.transition = 'none';
        slider.style.transform = `translateX(${base + offset}px)`;
      }
    }
  }, { passive: false });

  const handleTouchEnd = (e) => {
    if (!isTouching) return;
    isTouching = false;
    if (state.reader.mode !== 'paginated') return;

    const slider = document.getElementById('reader-columns-slider');
    if (slider) slider.style.transition = 'transform 0.26s cubic-bezier(0.2, 0, 0.2, 1)';

    const curX = e.changedTouches && e.changedTouches[0] ? e.changedTouches[0].clientX : startX;
    const curY = e.changedTouches && e.changedTouches[0] ? e.changedTouches[0].clientY : startY;
    const dx = curX - startX;
    const dy = curY - startY;
    const dt = Date.now() - startTime;

    // Horizontal swipe threshold: distance > 35px or quick flick
    const isSwipe = Math.abs(dx) > 35 || (dt < 250 && Math.abs(dx) > 20 && Math.abs(dx) > Math.abs(dy));
    if (isSwipe && isHorizontalMove) {
      if (dx < 0) {
        nextPage();
      } else {
        prevPage();
      }
      return;
    }

    // Snap back
    goToPage(state.reader.pageIndex, true);

    // Tap zone detection
    if (Math.abs(dx) < 12 && Math.abs(dy) < 12 && dt < 400) {
      const target = e.target;
      if (target && target.closest('a, button, input, select, textarea')) return;
      const sel = window.getSelection();
      if (sel && sel.toString().trim().length > 0) return;

      const vp = document.getElementById('reader-page-viewport') || container;
      const rect = vp.getBoundingClientRect();
      const relX = curX - rect.left;
      const w = rect.width;

      if (relX < w * 0.22) {
        prevPage();
      } else if (relX > w * 0.78) {
        nextPage();
      } else {
        toggleReaderChrome();
      }
    }
  };

  container.addEventListener('touchend', handleTouchEnd);
  container.addEventListener('touchcancel', () => {
    isTouching = false;
    goToPage(state.reader.pageIndex, true);
  });
}

function bindReaderDesktopClicks() {
  const vp = document.getElementById('reader-page-viewport');
  if (!vp) return;
  vp.addEventListener('click', (e) => {
    if (state.reader.mode !== 'paginated') return;
    if (e.target.closest('a, button, input, select, textarea')) return;
    const sel = window.getSelection();
    if (sel && sel.toString().trim().length > 0) return;

    const rect = vp.getBoundingClientRect();
    const relX = e.clientX - rect.left;
    const w = rect.width;
    if (relX < w * 0.18) {
      prevPage();
    } else if (relX > w * 0.82) {
      nextPage();
    } else {
      toggleReaderChrome();
    }
  });
}

function initReaderPreferences() {
  const savedMode = localStorage.getItem('folio_reader_mode') || 'paginated';
  const savedTheme = localStorage.getItem('folio_reader_theme') || 'paper';
  const savedFont = localStorage.getItem('folio_reader_font_family') || 'serif';
  const savedMeasure = localStorage.getItem('folio_reader_measure') || '65ch';

  state.reader.mode = savedMode;
  state.reader.theme = savedTheme;
  state.reader.fontFamily = savedFont;
  state.reader.measure = savedMeasure;

  document.querySelectorAll('[data-reader-theme]').forEach((btn) => {
    btn.classList.toggle('active', btn.getAttribute('data-reader-theme') === savedTheme);
  });
  document.getElementById('reader-scroll-container').setAttribute('data-reader-theme', savedTheme);

  document.querySelectorAll('[data-reader-font]').forEach((btn) => {
    btn.classList.toggle('active', btn.getAttribute('data-reader-font') === savedFont);
  });
  const fontMap = {
    serif: "var(--font-serif)",
    classic: "Georgia, 'Times New Roman', serif",
    sans: "var(--font-sans)",
    mono: "var(--font-mono)",
  };
  document.getElementById('reader-article').style.fontFamily = fontMap[savedFont] || fontMap.serif;

  setReaderMode(savedMode);
}

function bindReaderEvents() {
  document.getElementById('btn-close-reader').addEventListener('click', async () => {
    document.getElementById('web-reader-overlay').classList.add('hidden');
    document.getElementById('reader-typo-popover').classList.add('hidden');
    await refreshAll();
  });

  document.getElementById('btn-toggle-toc').addEventListener('click', () => {
    document.getElementById('reader-toc-sidebar').classList.toggle('hidden');
  });

  document.getElementById('tab-btn-toc').addEventListener('click', () => {
    document.getElementById('tab-btn-toc').classList.add('active');
    document.getElementById('tab-btn-bookmarks').classList.remove('active');
    document.getElementById('reader-toc-list').classList.remove('hidden');
    document.getElementById('reader-bookmarks-list').classList.add('hidden');
  });

  document.getElementById('tab-btn-bookmarks').addEventListener('click', () => {
    document.getElementById('tab-btn-bookmarks').classList.add('active');
    document.getElementById('tab-btn-toc').classList.remove('active');
    document.getElementById('reader-bookmarks-list').classList.remove('hidden');
    document.getElementById('reader-toc-list').classList.add('hidden');
  });

  document.getElementById('btn-prev-chapter').addEventListener('click', () => {
    loadReaderChapter(state.reader.chapterIndex - 1, 0, 0);
  });

  document.getElementById('btn-next-chapter').addEventListener('click', () => {
    loadReaderChapter(state.reader.chapterIndex + 1, 0, 0);
  });

  document.getElementById('btn-reader-prev-page').addEventListener('click', (e) => {
    e.stopPropagation();
    prevPage();
  });

  document.getElementById('btn-reader-next-page').addEventListener('click', (e) => {
    e.stopPropagation();
    nextPage();
  });

  document.getElementById('reader-scroll-container').addEventListener('scroll', () => {
    if (state.reader.mode === 'scroll') {
      updateReaderProgressMetrics(true);
    }
  });

  document.getElementById('reader-progress-track').addEventListener('click', (e) => {
    const track = document.getElementById('reader-progress-track');
    const rect = track.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    seekToGlobalPercentage(ratio);
  });

  bindReaderTouchAndSwipe();
  bindReaderDesktopClicks();

  document.getElementById('btn-reader-sync-now').addEventListener('click', () => {
    updateReaderProgressMetrics(true);
    showToast('Progress synced with KOReader / CrossPoint sync server!');
  });

  document.getElementById('btn-reader-bookmark').addEventListener('click', async () => {
    const { book, manifest, chapterIndex, pageIndex, totalPages, mode } = state.reader;
    if (!book || !manifest) return;
    const ch = manifest.chapters[chapterIndex];
    const xpathText = document.getElementById('reader-xpath-display').textContent.replace('KOSync XPath: ', '');
    const pctText = document.getElementById('reader-percent-display').textContent.replace('%', '');
    const pct = (parseFloat(pctText) || 0) / 100.0;
    const pageStr = mode === 'paginated' ? ` (p. ${pageIndex + 1}/${totalPages})` : '';

    const bmItem = {
      xpath: xpathText,
      percentage: pct,
      summary: `${ch.title}${pageStr} • ${(pct * 100).toFixed(1)}%`,
      si: chapterIndex,
      pp: pageIndex + 1,
      pc: totalPages || 10,
    };

    const res = await fetch(`/api/v1/bookmarks/${book.ko_md5_partial}`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Folio-Web': '1',
      },
      body: JSON.stringify({ items: [bmItem] }),
    });
    if (res.ok) {
      state.reader.bookmarks.push(bmItem);
      renderReaderBookmarks();
      showToast('Bookmark saved & queued for CrossPoint delta-sync!');
    }
  });

  document.getElementById('btn-reader-push-x3').addEventListener('click', () => {
    if (state.reader.book) {
      openX3ModalForBook(state.reader.book.id);
    }
  });

  // Typography & reading mode popover controls
  document.getElementById('btn-reader-typo').addEventListener('click', () => {
    document.getElementById('reader-typo-popover').classList.toggle('hidden');
  });

  document.querySelectorAll('[data-reader-mode]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const mode = btn.getAttribute('data-reader-mode');
      setReaderMode(mode);
    });
  });

  document.querySelectorAll('[data-reader-theme]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const t = btn.getAttribute('data-reader-theme');
      state.reader.theme = t;
      localStorage.setItem('folio_reader_theme', t);
      document.querySelectorAll('.theme-swatch[data-reader-theme]').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      document.getElementById('reader-scroll-container').setAttribute('data-reader-theme', t);
    });
  });

  document.querySelectorAll('[data-reader-font]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const f = btn.getAttribute('data-reader-font');
      state.reader.fontFamily = f;
      localStorage.setItem('folio_reader_font_family', f);
      document.querySelectorAll('.font-choice-btn').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      const map = {
        serif: "var(--font-serif)",
        classic: "Georgia, 'Times New Roman', serif",
        sans: "var(--font-sans)",
        mono: "var(--font-mono)",
      };
      document.getElementById('reader-article').style.fontFamily = map[f] || map.serif;
      if (state.reader.mode === 'paginated') {
        setTimeout(updateReaderPaginationGeometry, 30);
      }
    });
  });

  document.getElementById('btn-font-smaller').addEventListener('click', () => {
    state.reader.fontSize = Math.max(14, state.reader.fontSize - 1);
    applyReaderTypography();
  });

  document.getElementById('btn-font-larger').addEventListener('click', () => {
    state.reader.fontSize = Math.min(28, state.reader.fontSize + 1);
    applyReaderTypography();
  });

  document.getElementById('btn-width-narrow').addEventListener('click', () => {
    state.reader.measure = '54ch';
    applyReaderTypography();
  });
  document.getElementById('btn-width-normal').addEventListener('click', () => {
    state.reader.measure = '65ch';
    applyReaderTypography();
  });
  document.getElementById('btn-width-wide').addEventListener('click', () => {
    state.reader.measure = '80ch';
    applyReaderTypography();
  });

  // Responsive resize handler
  let resizeTimer = null;
  window.addEventListener('resize', () => {
    const readerOpen = !document.getElementById('web-reader-overlay').classList.contains('hidden');
    if (!readerOpen || state.reader.mode !== 'paginated') return;
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      const prevRatio = state.reader.totalPages > 1 ? state.reader.pageIndex / (state.reader.totalPages - 1) : 0;
      updateReaderPaginationGeometry();
      const targetPage = Math.min(state.reader.totalPages - 1, Math.round(prevRatio * (state.reader.totalPages - 1)));
      goToPage(targetPage, false);
    }, 100);
  });

  // Keyboard navigation inside reader
  window.addEventListener('keydown', (e) => {
    const readerOpen = !document.getElementById('web-reader-overlay').classList.contains('hidden');
    if (!readerOpen || isInputFocused()) return;

    if (e.key === 'ArrowRight' || e.key === 'PageDown' || (e.key === ' ' && !e.shiftKey)) {
      e.preventDefault();
      if (state.reader.mode === 'paginated') {
        nextPage();
      } else {
        const scroller = document.getElementById('reader-scroll-container');
        scroller.scrollBy({ top: scroller.clientHeight * 0.8, behavior: 'smooth' });
      }
    } else if (e.key === 'ArrowLeft' || e.key === 'PageUp' || (e.key === ' ' && e.shiftKey)) {
      e.preventDefault();
      if (state.reader.mode === 'paginated') {
        prevPage();
      } else {
        const scroller = document.getElementById('reader-scroll-container');
        scroller.scrollBy({ top: -scroller.clientHeight * 0.8, behavior: 'smooth' });
      }
    } else if (e.key === 'm' || e.key === 'M') {
      const nextMode = state.reader.mode === 'paginated' ? 'scroll' : 'paginated';
      setReaderMode(nextMode);
      showToast(`Switched to ${nextMode === 'paginated' ? 'Book (Paginated)' : 'Continuous Scroll'} mode`);
    } else if (e.key === 'Escape') {
      document.getElementById('btn-close-reader').click();
    }
  });
}

function applyReaderTypography() {
  const article = document.getElementById('reader-article');
  const viewport = document.getElementById('reader-page-viewport');
  if (!article) return;

  article.style.fontSize = `${state.reader.fontSize}px`;
  document.getElementById('reader-font-size-val').textContent = `${state.reader.fontSize}px`;

  const measureMap = {
    '54ch': { scrollMax: '54ch', paginatedMax: '640px' },
    '65ch': { scrollMax: '65ch', paginatedMax: '800px' },
    '80ch': { scrollMax: '80ch', paginatedMax: '980px' },
  };
  const m = measureMap[state.reader.measure] || measureMap['65ch'];
  article.style.maxWidth = m.scrollMax;
  if (viewport) {
    viewport.style.setProperty('--reader-paginated-max-width', m.paginatedMax);
  }

  localStorage.setItem('folio_reader_font_size', state.reader.fontSize);
  localStorage.setItem('folio_reader_measure', state.reader.measure);

  if (state.reader.mode === 'paginated') {
    const prevRatio = state.reader.totalPages > 1 ? state.reader.pageIndex / (state.reader.totalPages - 1) : 0;
    setTimeout(() => {
      updateReaderPaginationGeometry();
      const targetPage = Math.min(state.reader.totalPages - 1, Math.round(prevRatio * (state.reader.totalPages - 1)));
      goToPage(targetPage, false);
    }, 25);
  }
}

// --- CrossPoint Reader Companion ---

async function loadDevices() {
  try {
    const res = await fetch('/api/devices');
    if (!res.ok) return;
    const data = await res.json();
    state.devices = data.devices || [];

    const select = document.getElementById('x3-device-select');
    select.innerHTML = '';

    state.devices.forEach((d) => {
      const opt = document.createElement('option');
      opt.value = d.id;
      opt.textContent = `${d.name} (${d.host}:${d.http_port} / WS :${d.ws_port})`;
      select.appendChild(opt);
    });

    if (state.devices.length > 0) {
      if (!state.selectedDeviceId || !state.devices.some((d) => d.id === state.selectedDeviceId)) {
        state.selectedDeviceId = state.devices[0].id;
      }
      select.value = state.selectedDeviceId;
      const activeDev = state.devices.find((d) => d.id === Number(state.selectedDeviceId)) || state.devices[0];
      document.getElementById('sidebar-x3-summary').textContent = `${activeDev.host} • WS :${activeDev.ws_port}`;
      document.getElementById('x3-push-dest-path').value = activeDev.target_path || '/Books';
    }
  } catch (err) {
    console.error('Failed to load devices:', err);
  }
}

function populateX3BookSelect(selectedBookId) {
  const select = document.getElementById('x3-push-book-select');
  if (!select) return;
  select.innerHTML = '';
  state.allBooksSnapshot.forEach((b) => {
    const opt = document.createElement('option');
    opt.value = b.id;
    opt.textContent = `${b.title} — ${b.authors} (${formatBytes(b.file_size)})`;
    select.appendChild(opt);
  });
  if (selectedBookId) {
    select.value = selectedBookId;
  }
}

async function openX3ModalForBook(bookId) {
  populateX3BookSelect(bookId);
  document.getElementById('x3-modal-backdrop').classList.remove('hidden');
  await probeSelectedX3Device();
  await loadSelectedX3Files();
}

async function probeSelectedX3Device() {
  const devId = document.getElementById('x3-device-select').value;
  if (!devId) return;

  const onlineEl = document.getElementById('x3-stat-online');
  onlineEl.textContent = 'Probing...';

  try {
    const res = await fetch(`/api/devices/${devId}/status`);
    const data = await res.json();
    if (data.online && data.status) {
      const st = data.status;
      onlineEl.textContent = 'Online (File Transfer Ready)';
      onlineEl.className = 'telemetry-val status-online';
      document.getElementById('x3-stat-device').textContent = st.device ? `CrossPoint (${st.device})` : 'CrossPoint Reader';
      document.getElementById('x3-stat-fw').textContent = `${st.firmwareFamily || 'crosspoint'} v${st.version}`;
      document.getElementById('x3-stat-wifi').textContent = `${st.mode} (${st.rssi} dBm)`;
      document.getElementById('x3-stat-heap').textContent = `${Math.round((st.freeHeap || 0) / 1024)} KB`;
      document.getElementById('sidebar-x3-dot').classList.add('online');
    } else {
      onlineEl.textContent = 'Unreachable (Enable File Transfer on device)';
      onlineEl.className = 'telemetry-val status-offline';
    }
  } catch (err) {
    onlineEl.textContent = 'Offline';
    onlineEl.className = 'telemetry-val status-offline';
  }
}

async function loadSelectedX3Files() {
  const devId = document.getElementById('x3-device-select').value;
  const listEl = document.getElementById('x3-sd-file-list');
  if (!devId) return;

  const targetDir = document.getElementById('x3-push-dest-path').value || '/Books';
  document.getElementById('x3-current-sd-path').textContent = targetDir;

  try {
    const res = await fetch(`/api/devices/${devId}/files?path=${encodeURIComponent(targetDir)}`);
    if (!res.ok) {
      listEl.innerHTML = `<div style="padding:12px; color:var(--text-muted);">Device SD card offline or folder not created yet.</div>`;
      return;
    }
    const data = await res.json();
    const files = data.files || [];
    listEl.innerHTML = '';

    if (files.length === 0) {
      listEl.innerHTML = `<div style="padding:14px; color:var(--text-muted); font-size:12.5px;">Folder <code>${escapeHTML(
        targetDir
      )}</code> is empty on the CrossPoint Reader SD card. Push a book to see it here!</div>`;
      return;
    }

    files.forEach((f) => {
      const row = document.createElement('div');
      row.className = 'sd-file-row';
      row.innerHTML = `
        <div class="sd-file-name" title="${escapeHTML(f.name)}">
          ${f.isDirectory ? '📁' : '📘'} ${escapeHTML(f.name)}
        </div>
        <div style="display:flex; align-items:center; gap:8px;">
          <span class="mono-val" style="font-size:11px; color:var(--text-muted);">${
            f.isDirectory ? 'DIR' : formatBytes(f.size)
          }</span>
          ${
            !f.isDirectory
              ? `<button type="button" class="btn btn-xs btn-danger-ghost sd-del-btn">Delete</button>`
              : ''
          }
        </div>
      `;
      const delBtn = row.querySelector('.sd-del-btn');
      if (delBtn) {
        delBtn.addEventListener('click', async () => {
          const fullPath = `${targetDir.replace(/\/$/, '')}/${f.name}`;
          const delRes = await fetch(`/api/devices/${devId}/delete-file`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ path: fullPath }),
          });
          if (delRes.ok) {
            showToast(`Deleted ${f.name} from CrossPoint Reader SD card.`);
            await loadSelectedX3Files();
          }
        });
      }
      listEl.appendChild(row);
    });
  } catch (err) {
    listEl.innerHTML = `<div style="padding:12px; color:var(--text-muted);">Could not list SD card files.</div>`;
  }
}

function bindX3ModalEvents() {
  const openModal = () => {
    populateX3BookSelect(state.activeBook ? state.activeBook.id : undefined);
    document.getElementById('x3-modal-backdrop').classList.remove('hidden');
    probeSelectedX3Device();
    loadSelectedX3Files();
  };

  document.getElementById('btn-open-x3-hub').addEventListener('click', openModal);
  document.getElementById('chip-x3').addEventListener('click', openModal);

  document.getElementById('btn-close-x3-modal').addEventListener('click', () => {
    document.getElementById('x3-modal-backdrop').classList.add('hidden');
  });

  document.getElementById('x3-device-select').addEventListener('change', (e) => {
    state.selectedDeviceId = Number(e.target.value);
    probeSelectedX3Device();
    loadSelectedX3Files();
  });

  document.getElementById('btn-x3-refresh-status').addEventListener('click', () => {
    probeSelectedX3Device();
    loadSelectedX3Files();
  });

  document.getElementById('btn-x3-refresh-files').addEventListener('click', loadSelectedX3Files);

  // UDP Port 8134 Discovery
  document.getElementById('btn-x3-discover').addEventListener('click', async () => {
    const logEl = document.getElementById('x3-protocol-log');
    logEl.textContent = 'Broadcasting UDP "hello" on port 8134 to discover CrossPoint Reader devices...';
    const res = await fetch('/api/devices/discover', { method: 'POST' });
    const data = await res.json();
    const list = data.discovered || [];
    if (list.length === 0) {
      logEl.textContent = 'No CrossPoint Reader response on UDP 8134. Ensure File Transfer mode is open on your device.';
      showToast('No additional CrossPoint devices discovered on UDP.');
    } else {
      const lines = list.map((d) => `Discovered: ${d.hostname} at ${d.host} (HTTP :${d.http_port}, WS :${d.ws_port}) -> "${d.raw_reply}"`);
      logEl.textContent = lines.join('\n');
      showToast(`Discovered ${list.length} CrossPoint Reader device(s) on LAN!`);
    }
  });

  // Add/Edit Device Form
  const formEl = document.getElementById('x3-device-form');
  document.getElementById('btn-x3-toggle-add').addEventListener('click', () => {
    formEl.classList.toggle('hidden');
  });
  document.getElementById('btn-x3-cancel-form').addEventListener('click', () => {
    formEl.classList.add('hidden');
  });

  formEl.addEventListener('submit', async (e) => {
    e.preventDefault();
    const payload = {
      name: document.getElementById('x3-form-name').value,
      host: document.getElementById('x3-form-host').value,
      http_port: parseInt(document.getElementById('x3-form-http-port').value, 10) || 80,
      ws_port: parseInt(document.getElementById('x3-form-ws-port').value, 10) || 81,
      target_path: document.getElementById('x3-form-path').value || '/Books',
      preferred_protocol: document.getElementById('x3-form-proto').value || 'auto',
      device_model: 'CrossPoint Reader',
      is_default: true,
    };
    const res = await fetch('/api/devices', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (res.ok) {
      const data = await res.json();
      state.selectedDeviceId = data.device.id;
      formEl.classList.add('hidden');
      showToast(`Saved CrossPoint device "${data.device.name}"!`);
      await loadDevices();
      await probeSelectedX3Device();
      await loadSelectedX3Files();
    }
  });

  // Toggle optimizer sub-options state when master checkbox changes
  const optEnabledEl = document.getElementById('x3-opt-enabled');
  if (optEnabledEl) {
    optEnabledEl.addEventListener('change', () => {
      const disabled = !optEnabledEl.checked;
      ['x3-opt-target', 'x3-opt-grayscale', 'x3-opt-split', 'x3-opt-autocrop'].forEach((id) => {
        const el = document.getElementById(id);
        if (el) el.disabled = disabled;
      });
    });
  }

  // Push EPUB to CrossPoint Reader
  document.getElementById('btn-execute-push').addEventListener('click', async () => {
    const devId = Number(document.getElementById('x3-device-select').value);
    const bookId = Number(document.getElementById('x3-push-book-select').value);
    const targetPath = document.getElementById('x3-push-dest-path').value || '/Books';
    const protocol = document.getElementById('x3-push-protocol').value || 'auto';

    const optimize = document.getElementById('x3-opt-enabled') ? document.getElementById('x3-opt-enabled').checked : true;
    const deviceTarget = document.getElementById('x3-opt-target') ? document.getElementById('x3-opt-target').value : 'auto';
    const grayscale = document.getElementById('x3-opt-grayscale') ? document.getElementById('x3-opt-grayscale').checked : true;
    const splitText = document.getElementById('x3-opt-split') ? document.getElementById('x3-opt-split').checked : true;
    const autoCrop = document.getElementById('x3-opt-autocrop') ? document.getElementById('x3-opt-autocrop').checked : false;

    if (!devId || !bookId) {
      showToast('Select both a target CrossPoint Reader device and a book.');
      return;
    }

    const logEl = document.getElementById('x3-protocol-log');
    const badgeEl = document.getElementById('x3-transfer-badge');
    badgeEl.textContent = optimize ? 'Optimizing & Streaming...' : 'Streaming...';
    logEl.textContent = `${optimize ? 'Optimizing EPUB for CrossPoint Reader and initiating' : 'Initiating'} ${protocol.toUpperCase()} upload to CrossPoint Reader (${targetPath})...`;

    // Browser-Direct mode (useful when Folio runs in cloud/Docker and browser is on local Wi-Fi with the device)
    if (protocol === 'browser') {
      await pushBrowserDirectToX3(devId, bookId, targetPath, logEl, badgeEl, {
        optimize,
        deviceTarget,
        grayscale,
        splitText,
        autoCrop,
      });
      return;
    }

    try {
      const res = await fetch(`/api/devices/${devId}/push`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          book_id: bookId,
          target_path: targetPath,
          protocol: protocol,
          overwrite: true,
          optimize: optimize,
          device_target: deviceTarget,
          grayscale: grayscale,
          split_text: splitText,
          auto_crop: autoCrop,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        badgeEl.textContent = 'Error';
        logEl.textContent = `ERROR: ${data.error || 'Upload failed'}`;
        showToast(`CrossPoint Push Error: ${data.error}`);
        return;
      }

      const r = data.result;
      badgeEl.textContent = `${r.protocol.toUpperCase()} • ${r.duration_ms}ms`;
      logEl.textContent = [...(r.steps || []), '', `✓ ${r.message}`].join('\n');
      showToast(`Pushed "${data.book.title}" to ${data.device.name}!`);
      await loadSelectedX3Files();
    } catch (err) {
      badgeEl.textContent = 'Failed';
      logEl.textContent = `Network error: ${err.message}`;
    }
  });

  // One-click OPDS & KOSync provisioning onto CrossPoint Reader
  document.getElementById('btn-x3-provision-opds').addEventListener('click', async () => {
    const devId = document.getElementById('x3-device-select').value;
    if (!devId) return;
    const res = await fetch(`/api/devices/${devId}/configure-opds`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    });
    const data = await res.json();
    if (res.ok) {
      document.getElementById('x3-protocol-log').textContent = `POST /api/opds -> Registered "${data.catalog_url}"\nPOST /api/settings -> Set koServerUrl="${data.kosync_url}"\n✓ ${data.message}`;
      showToast('Configured Folio OPDS catalog & KOReader Sync on CrossPoint Reader!');
    } else {
      showToast(`Provisioning failed: ${data.error}`);
    }
  });
}

async function pushBrowserDirectToX3(devId, bookId, targetPath, logEl, badgeEl, optCfg = {}) {
  const dev = state.devices.find((d) => d.id === devId);
  const book = state.allBooksSnapshot.find((b) => b.id === bookId);
  if (!dev || !book) return;

  try {
    const params = new URLSearchParams();
    if (optCfg.optimize !== false) {
      params.set('optimize', 'crosspoint');
      if (optCfg.deviceTarget) params.set('device_target', optCfg.deviceTarget);
      if (optCfg.grayscale === false) params.set('grayscale', '0');
      if (optCfg.splitText === false) params.set('split_text', '0');
      if (optCfg.autoCrop === true) params.set('auto_crop', '1');
    } else {
      params.set('optimize', '0');
    }
    const qs = params.toString() ? `?${params.toString()}` : '';
    logEl.textContent = `1. Fetching ${optCfg.optimize !== false ? 'CrossPoint-optimized ' : ''}${book.file_name} from Folio server...\n`;
    const epubRes = await fetch(`/api/books/${book.id}/download/${encodeURIComponent(book.file_name)}${qs}`);
    const blob = await epubRes.blob();

    const fd = new FormData();
    fd.append('file', blob, book.file_name);
    const x3Url = `http://${dev.host}:${dev.http_port}/upload?path=${encodeURIComponent(targetPath)}&overwrite=1`;
    logEl.textContent += `2. Uploading ${blob.size} bytes directly from browser to ${x3Url}...\n`;

    const upRes = await fetch(x3Url, { method: 'POST', body: fd });
    const upText = await upRes.text();
    if (!upRes.ok) {
      throw new Error(upText || `HTTP ${upRes.status}`);
    }
    badgeEl.textContent = 'Browser-Direct OK';
    logEl.textContent += `3. RX <- ${upText}\n✓ Browser-Direct transfer to CrossPoint Reader complete!`;
    showToast(`Pushed "${book.title}" directly from browser to CrossPoint Reader!`);
    await loadSelectedX3Files();
  } catch (err) {
    badgeEl.textContent = 'Browser Push Error';
    logEl.textContent += `ERROR: ${err.message}`;
  }
}

// --- KOReader Sync & OPDS Hub Modal ---

function bindSyncModalEvents() {
  const openSyncModal = async () => {
    await loadSystemInfo();
    document.getElementById('sync-modal-backdrop').classList.remove('hidden');
  };

  document.getElementById('btn-open-sync-hub').addEventListener('click', openSyncModal);
  document.getElementById('chip-opds').addEventListener('click', openSyncModal);
  document.getElementById('chip-kosync').addEventListener('click', openSyncModal);

  document.getElementById('btn-close-sync-modal').addEventListener('click', () => {
    document.getElementById('sync-modal-backdrop').classList.add('hidden');
  });

  document.getElementById('sync-user-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const username = document.getElementById('sync-user-input').value.trim();
    const password = document.getElementById('sync-pass-input').value.trim();
    const res = await fetch('/api/system/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
    if (res.ok) {
      const data = await res.json();
      showToast(`Saved KOReader sync user "${username}" (x-auth-key: ${data.password_md5.slice(0, 10)}…)`);
      await loadSystemInfo();
    }
  });

  // Simulate a CrossPoint / KOReader device syncing progress to verify end-to-end kosync
  document.getElementById('btn-simulate-kosync').addEventListener('click', async () => {
    if (!state.allBooksSnapshot.length) return;
    const book = state.allBooksSnapshot[0];
    const currentPct = book.latest_progress ? book.latest_progress.percentage : 0.12;
    const nextPct = currentPct >= 0.9 ? 0.25 : Number((currentPct + 0.14).toFixed(4));
    const para = Math.max(1, Math.round(nextPct * 24));
    const xpath = `/body/DocFragment[2]/body/section[1]/p[${para}]/text().0`;

    const res = await fetch('/syncs/progress', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Folio-Web': '1',
      },
      body: JSON.stringify({
        document: book.ko_md5_partial,
        progress: xpath,
        percentage: nextPct,
        device: 'CrossPoint Reader',
        device_id: 'crosspoint-reader',
        position: {
          pctQ: Math.round(nextPct * 1000000),
          spine: 1,
          page: para,
          pages: 28,
          para: para,
          xpath: xpath,
        },
        metadata: {
          filename: book.file_name,
          title: book.title,
          authors: book.authors,
        },
      }),
    });
    if (res.ok) {
      showToast(`Synced "${book.title}" to ${(nextPct * 100).toFixed(1)}% from CrossPoint Reader!`);
      await refreshAll();
    }
  });
}

function renderSyncActivity(progressList) {
  const container = document.getElementById('sync-activity-list');
  if (!container) return;
  container.innerHTML = '';

  if (!progressList || progressList.length === 0) {
    container.innerHTML = `<div style="padding:14px; color:var(--text-muted); font-size:12.5px;">No reading progress synced yet. Read a book or click "Simulate Device Sync" to test KOReader sync.</div>`;
    return;
  }

  progressList.forEach((rp) => {
    const pct = (rp.percentage * 100).toFixed(1);
    const title = rp.book_title || `Document ${rp.document.slice(0, 10)}…`;
    const dateStr = new Date(rp.timestamp * 1000).toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });

    const row = document.createElement('div');
    row.className = 'sync-event-row';
    row.innerHTML = `
      <div style="min-width:0;">
        <div style="font-weight:600; font-size:13px;">${escapeHTML(title)}</div>
        <div class="mono-val" style="font-size:11px; color:var(--text-muted); overflow:hidden; text-overflow:ellipsis;">
          ${escapeHTML(rp.device)} &bull; ${escapeHTML(rp.progress)}
        </div>
      </div>
      <div style="text-align:right; flex-shrink:0;">
        <div class="mono-pill" style="background:var(--accent-subtle); color:var(--accent-primary); font-weight:600;">${pct}%</div>
        <div class="mono-val" style="font-size:10.5px; color:var(--text-muted); margin-top:2px;">${dateStr}</div>
      </div>
    `;
    container.appendChild(row);
  });
}

// --- Tailscale Zero-Trust Identity & Device Passkeys ---

function renderTailscaleSession(ts) {
  if (!ts) return;
  const login = ts.login_name || 'reader@tailnet.ts.net';
  const ip = ts.tailscale_ip || '100.101.102.103';
  const node = ts.node_name || 'folio-reader.tailnet.ts.net';
  const source = ts.source || 'tailscale-auto-dev';

  const sidebarUser = document.getElementById('sidebar-ts-user');
  const sidebarSummary = document.getElementById('sidebar-ts-summary');
  const chipLabel = document.getElementById('chip-tailscale-label');
  const authBadge = document.getElementById('sys-auth-badge');

  if (sidebarUser) {
    sidebarUser.textContent = login;
    sidebarUser.title = login;
  }
  if (sidebarSummary) {
    sidebarSummary.textContent = `${ip} • ${source}`;
    sidebarSummary.title = `${ip} • ${source}`;
  }
  if (chipLabel) chipLabel.textContent = `Tailscale: ${login}`;
  if (authBadge) authBadge.textContent = `Tailscale (${ts.auth_mode || 'auto'})`;

  // Populate modal telemetry
  const elState = document.getElementById('ts-stat-state');
  const elSource = document.getElementById('ts-stat-source');
  const elLogin = document.getElementById('ts-stat-login');
  const elNode = document.getElementById('ts-stat-node');
  const elIP = document.getElementById('ts-stat-ip');
  const elMode = document.getElementById('ts-stat-mode');

  if (elState) elState.textContent = ts.authenticated ? 'Authenticated' : 'Unauthenticated';
  if (elSource) elSource.textContent = source;
  if (elLogin) elLogin.textContent = login;
  if (elNode) elNode.textContent = node;
  if (elIP) elIP.textContent = ip;
  if (elMode) elMode.textContent = ts.auth_mode || 'auto';

  if (ts.user) {
    const syncUserEl = document.getElementById('ts-sync-user');
    const syncMd5El = document.getElementById('ts-sync-md5');
    if (syncUserEl) syncUserEl.textContent = ts.user.username || login;
    if (syncMd5El) syncMd5El.textContent = ts.user.password_key || '';
    const syncInput = document.getElementById('sync-user-input');
    if (syncInput) syncInput.value = ts.user.username || 'reader';
  }

  // Form defaults
  const modeSelect = document.getElementById('ts-form-mode');
  const loginInput = document.getElementById('ts-form-login');
  const nameInput = document.getElementById('ts-form-name');
  const nodeInput = document.getElementById('ts-form-node');
  const ipInput = document.getElementById('ts-form-ip');

  if (modeSelect && ts.auth_mode) modeSelect.value = ts.auth_mode;
  if (loginInput && ts.login_name) loginInput.value = ts.login_name;
  if (nameInput && ts.display_name) nameInput.value = ts.display_name;
  if (nodeInput && ts.node_name) nodeInput.value = ts.node_name;
  if (ipInput && ts.tailscale_ip) ipInput.value = ts.tailscale_ip;

  const jsonBox = document.getElementById('ts-session-json');
  if (jsonBox) {
    jsonBox.textContent = JSON.stringify(ts, null, 2);
  }
  const sockBadge = document.getElementById('ts-socket-badge');
  if (sockBadge) {
    sockBadge.textContent = ts.socket_connected
      ? 'tailscaled.sock Connected'
      : `Mode: ${ts.auth_mode} (${source})`;
  }
}

function bindTailscaleModalEvents() {
  const openModal = async () => {
    document.getElementById('tailscale-modal-backdrop').classList.remove('hidden');
    const res = await fetch('/api/auth/tailscale');
    if (res.ok) {
      const sess = await res.json();
      renderTailscaleSession(sess);
    }
  };

  const btnSidebar = document.getElementById('btn-open-tailscale-hub');
  const btnChip = document.getElementById('chip-tailscale');
  const btnClose = document.getElementById('btn-close-tailscale-modal');

  if (btnSidebar) btnSidebar.addEventListener('click', openModal);
  if (btnChip) btnChip.addEventListener('click', openModal);
  if (btnClose) {
    btnClose.addEventListener('click', () => {
      document.getElementById('tailscale-modal-backdrop').classList.add('hidden');
    });
  }

  const simForm = document.getElementById('ts-sim-form');
  if (simForm) {
    simForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      const payload = {
        action: 'switch_identity',
        auth_mode: document.getElementById('ts-form-mode').value,
        login_name: document.getElementById('ts-form-login').value.trim(),
        display_name: document.getElementById('ts-form-name').value.trim(),
        node_name: document.getElementById('ts-form-node').value.trim(),
        tailscale_ip: document.getElementById('ts-form-ip').value.trim(),
      };
      const res = await fetch('/api/auth/tailscale', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (res.ok) {
        const sess = await res.json();
        renderTailscaleSession(sess);
        showToast(`Tailscale session active as ${sess.login_name} (${sess.tailscale_ip})`);
        await loadSystemInfo();
      }
    });
  }

  const btnTestHeaders = document.getElementById('btn-ts-test-headers');
  if (btnTestHeaders) {
    btnTestHeaders.addEventListener('click', async () => {
      const login = document.getElementById('ts-form-login').value.trim() || 'reader@tailnet.ts.net';
      const name = document.getElementById('ts-form-name').value.trim() || 'Tailscale Serve Reader';
      const node = document.getElementById('ts-form-node').value.trim() || 'folio-serve.tailnet.ts.net';
      const res = await fetch('/api/auth/tailscale', {
        headers: {
          'Tailscale-User-Login': login,
          'Tailscale-User-Name': name,
          'X-Tailscale-Node': node,
        },
      });
      if (res.ok) {
        const sess = await res.json();
        renderTailscaleSession(sess);
        showToast(`Verified Tailscale-User-Login header (${sess.login_name}, source: ${sess.source})!`);
      }
    });
  }

  const btnRotateKey = document.getElementById('btn-ts-rotate-key');
  if (btnRotateKey) {
    btnRotateKey.addEventListener('click', async () => {
      const customPass = document.getElementById('ts-new-passkey').value.trim();
      const res = await fetch('/api/auth/tailscale', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'rotate_passkey',
          new_password: customPass,
        }),
      });
      if (res.ok) {
        const data = await res.json();
        if (data.tailscale) {
          renderTailscaleSession(data.tailscale);
        }
        const hint = document.getElementById('ts-rotated-hint');
        if (hint) {
          hint.innerHTML = `New plaintext passkey: <code>${escapeHTML(data.plain_passkey)}</code> (MD5 <code>${escapeHTML(
            data.password_md5
          )}</code>)`;
        }
        showToast(`Rotated KOReader / CrossPoint passkey! New passkey: ${data.plain_passkey}`);
      }
    });
  }
}

// --- Utilities ---

function bindCopyButtons() {
  document.querySelectorAll('[data-copy-target]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const targetId = btn.getAttribute('data-copy-target');
      const targetEl = document.getElementById(targetId);
      if (!targetEl) return;
      navigator.clipboard.writeText(targetEl.textContent.trim());
      const orig = btn.textContent;
      btn.textContent = 'Copied!';
      setTimeout(() => {
        btn.textContent = orig;
      }, 1200);
    });
  });
}

function isInputFocused() {
  const el = document.activeElement;
  if (!el) return false;
  const tag = el.tagName.toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable;
}

function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 KB';
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function escapeHTML(str) {
  return String(str || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
