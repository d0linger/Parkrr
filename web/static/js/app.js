/* Parkrr SPA – vanilla JS, no external dependencies. */
(() => {
    'use strict';

    // ---------- utilities ----------
    const $ = (sel, root = document) => root.querySelector(sel);
    const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
    const el = (tag, attrs = {}, ...children) => {
        const node = document.createElement(tag);
        for (const [k, v] of Object.entries(attrs)) {
            if (k === 'class') node.className = v;
            else if (k === 'html') node.innerHTML = v;
            // Apply styles via the CSSOM (not a style="" attribute) so they are
            // not blocked by the strict `style-src 'self'` CSP.
            else if (k === 'style') node.style.cssText = v;
            else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2), v);
            else if (v === true) node.setAttribute(k, '');
            else if (v !== false && v != null) node.setAttribute(k, v);
        }
        for (const c of children.flat()) {
            if (c == null || c === false) continue;
            node.append(c.nodeType ? c : document.createTextNode(String(c)));
        }
        return node;
    };
    const esc = (s) => String(s ?? '');
    const getCookie = (name) => {
        const m = document.cookie.match('(^|;)\\s*' + name + '\\s*=\\s*([^;]+)');
        return m ? decodeURIComponent(m.pop()) : '';
    };
    const eur = (n) => new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(Number(n) || 0);
    const fmtDate = (s) => (s ? new Date(s).toLocaleDateString('de-DE') : '–');
    const fmtDateTime = (s) => (s ? new Date(s).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' }) : '–');
    const today = () => new Date().toISOString().slice(0, 10);
    const norm = (s) => String(s ?? '').toLowerCase();

    // ---------- i18n ----------
    // Central message catalogue. User-facing strings should be added here and
    // referenced via t('key'); this is the foundation for future localisation.
    const MESSAGES = {
        de: {
            'validation.required': 'Pflichtfeld',
            'validation.email': 'Bitte eine gültige E-Mail-Adresse eingeben',
            'validation.min': 'Mindestens {n} Zeichen',
            'validation.numberMin': 'Muss mindestens {n} sein',
            'validation.number': 'Bitte eine gültige Zahl eingeben',
            'offline.banner': 'Offline – Änderungen werden erst nach erneuter Verbindung möglich.',
            'offline.action': 'Keine Internetverbindung – bitte später erneut versuchen.',
        },
    };
    const LANG = (document.documentElement.lang || 'de').slice(0, 2);
    function t(key, params) {
        let s = (MESSAGES[LANG] && MESSAGES[LANG][key]) || MESSAGES.de[key] || key;
        if (params) for (const [k, v] of Object.entries(params)) s = s.split('{' + k + '}').join(v);
        return s;
    }
    const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

    // ---------- API ----------
    const api = {
        async request(method, path, body) {
            // Fail fast on write attempts while offline with a friendly message
            // rather than a confusing network error.
            if (method !== 'GET' && typeof navigator !== 'undefined' && navigator.onLine === false) {
                throw new Error(t('offline.action'));
            }
            const opts = { method, headers: { Accept: 'application/json' }, credentials: 'same-origin' };
            if (body !== undefined) {
                opts.headers['Content-Type'] = 'application/json';
                opts.body = JSON.stringify(body);
            }
            if (method !== 'GET') opts.headers['X-CSRF-Token'] = getCookie('parkrr_csrf');
            return handle(await fetch('/api' + path, opts));
        },
        async upload(path, formData) {
            const res = await fetch('/api' + path, {
                method: 'POST', body: formData, credentials: 'same-origin',
                headers: { 'X-CSRF-Token': getCookie('parkrr_csrf') },
            });
            return handle(res);
        },
        get: (p) => api.request('GET', p),
        post: (p, b) => api.request('POST', p, b),
        put: (p, b) => api.request('PUT', p, b),
        del: (p) => api.request('DELETE', p),
    };
    async function handle(res) {
        if (res.status === 204) return null;
        let data = null;
        const ct = res.headers.get('content-type') || '';
        if (ct.includes('application/json')) data = await res.json();
        if (!res.ok) {
            const err = new Error((data && data.error) || 'HTTP ' + res.status);
            err.status = res.status;
            err.data = data;
            throw err;
        }
        return data;
    }

    // ---------- state ----------
    const state = { user: null, persons: [], categories: [], services: [], capabilities: {} };

    // permission helpers
    const isAdmin = () => !!(state.user && state.user.is_admin);
    const role = () => (state.user ? state.user.role : '');
    // Editors may do everything except user management and the audit log.
    const canManage = () => isAdmin() || role() === 'editor';
    const canBill = () => isAdmin() || role() === 'editor';

    // ---------- theme ----------
    function initTheme() {
        const saved = localStorage.getItem('parkrr-theme');
        if (saved) document.documentElement.dataset.theme = saved;
    }
    function toggleTheme() {
        const cur = document.documentElement.dataset.theme;
        let next;
        if (cur) next = cur === 'dark' ? 'light' : 'dark';
        else next = matchMedia('(prefers-color-scheme: dark)').matches ? 'light' : 'dark';
        document.documentElement.dataset.theme = next;
        localStorage.setItem('parkrr-theme', next);
    }

    // ---------- toast ----------
    let toastTimer = null;
    function toast(msg, kind = '') {
        const t = $('#toast');
        t.innerHTML = '';
        t.append(document.createTextNode(msg));
        t.className = 'toast' + (kind ? ' ' + kind : '');
        t.hidden = false;
        clearTimeout(toastTimer);
        toastTimer = setTimeout(() => { t.hidden = true; }, 3200);
    }
    function toastAction(msg, actionLabel, onAction, ms = 4500) {
        const t = $('#toast');
        clearTimeout(toastTimer);
        t.innerHTML = '';
        t.className = 'toast';
        t.hidden = false;
        t.append(document.createTextNode(msg + '  '));
        const btn = el('button', { class: 'btn btn-sm btn-ghost', style: 'margin-left:.4rem' }, actionLabel);
        btn.addEventListener('click', () => { t.hidden = true; onAction(); });
        t.append(btn);
        toastTimer = setTimeout(() => { t.hidden = true; }, ms);
    }

    // ---------- confirm ----------
    function confirmDialog(title, message, okLabel = 'Löschen') {
        return new Promise((resolve) => {
            const dlg = $('#confirm');
            $('#confirm-title').textContent = title;
            $('#confirm-body').textContent = message;
            const ok = $('#confirm-ok'); const cancel = $('#confirm-cancel');
            ok.textContent = okLabel;
            const done = (v) => { ok.removeEventListener('click', onOk); cancel.removeEventListener('click', onCancel); dlg.close(); resolve(v); };
            const onOk = () => done(true); const onCancel = () => done(false);
            ok.addEventListener('click', onOk); cancel.addEventListener('click', onCancel);
            dlg.showModal();
        });
    }

    // delete with an undo window (delayed server call)
    async function deleteWithUndo(title, message, doDelete, onDone) {
        if (!await confirmDialog(title, message)) return;
        let cancelled = false;
        const timer = setTimeout(async () => {
            if (cancelled) return;
            try { await doDelete(); toast('Gelöscht', 'success'); onDone && onDone(); }
            catch (e) { toast(e.message, 'error'); onDone && onDone(); }
        }, 4500);
        toastAction('Wird gelöscht …', 'Rückgängig', () => { cancelled = true; clearTimeout(timer); toast('Abgebrochen'); });
    }

    // ---------- form modal ----------
    // Client-side validation mirroring the server's rules, for fast feedback.
    function validateField(f, rawValue) {
        if (f.type === 'checkbox' || f.type === 'select') return '';
        const v = String(rawValue ?? '').trim();
        if (f.required && !v) return t('validation.required');
        if (!v) return '';
        if (f.type === 'email' || f.name === 'email') {
            if (!EMAIL_RE.test(v)) return t('validation.email');
        }
        if (f.type === 'number') {
            const n = Number(v);
            if (!Number.isFinite(n)) return t('validation.number');
            if (f.min != null && n < Number(f.min)) return t('validation.numberMin', { n: f.min });
        }
        if (f.minLength && v.length < f.minLength) return t('validation.min', { n: f.minLength });
        return '';
    }

    function formModal({ title, fields, submitLabel = 'Speichern', onRender = null }) {
        return new Promise((resolve) => {
            const dlg = $('#modal');
            $('#modal-title').textContent = title;
            $('#modal-submit').textContent = submitLabel;
            $('#modal-submit').style.display = '';
            $('#modal-cancel').style.display = '';
            const body = $('#modal-body');
            body.innerHTML = '';
            for (const f of fields) {
                if (f.type === 'checkbox') {
                    const input = el('input', { type: 'checkbox', id: 'f_' + f.name, name: f.name });
                    if (f.value) input.checked = true;
                    body.append(el('label', { class: 'switch', for: 'f_' + f.name }, input, el('span', { class: 'track' }), el('span', {}, f.label)));
                    continue;
                }
                const id = 'f_' + f.name;
                body.append(el('label', { for: id }, f.label + (f.required ? ' *' : '')));
                let input;
                if (f.type === 'select') {
                    input = el('select', { id, name: f.name });
                    for (const o of f.options) {
                        const opt = el('option', { value: o.value }, o.label);
                        if (String(o.value) === String(f.value)) opt.selected = true;
                        input.append(opt);
                    }
                } else if (f.type === 'textarea') {
                    input = el('textarea', { id, name: f.name, placeholder: f.placeholder || '' }, f.value || '');
                } else {
                    input = el('input', { id, name: f.name, type: f.type || 'text', value: f.value != null ? f.value : '', placeholder: f.placeholder || '' });
                    if (f.required) input.required = true;
                    if (f.step) input.step = f.step;
                    if (f.min != null) input.min = f.min;
                    // Allow manual keyboard/numpad entry (important for back-dating
                    // older records). We deliberately do NOT auto-open the native
                    // picker on focus/click — that hijacked the field and blocked
                    // typing. Mobile still opens its native date UI on tap, and
                    // desktop shows the built-in calendar icon to click.
                }
                body.append(input);
                body.append(el('div', { class: 'field-error', id: 'err_' + f.name, role: 'alert', hidden: true }));
                if (f.help) body.append(el('div', { class: 'card-meta' }, f.help));
            }
            const form = $('#modal-form');
            const cleanup = () => {
                form.removeEventListener('submit', onSubmit);
                $('#modal-cancel').removeEventListener('click', onCancel);
                $('#modal-close').removeEventListener('click', onCancel);
            };
            const onCancel = () => { cleanup(); dlg.close(); resolve(null); };
            const onSubmit = (e) => {
                e.preventDefault();
                const data = {};
                let firstInvalid = null;
                for (const f of fields) {
                    const node = $('#f_' + f.name, body);
                    const value = f.type === 'checkbox' ? node.checked : node.value;
                    data[f.name] = value;
                    const msg = validateField(f, value);
                    const errNode = $('#err_' + f.name, body);
                    if (errNode) { errNode.textContent = msg; errNode.hidden = !msg; }
                    if (msg) {
                        node.setAttribute('aria-invalid', 'true');
                        if (!firstInvalid) firstInvalid = node;
                    } else {
                        node.removeAttribute('aria-invalid');
                    }
                }
                if (firstInvalid) { firstInvalid.focus(); return; }
                cleanup(); dlg.close(); resolve(data);
            };
            form.addEventListener('submit', onSubmit);
            $('#modal-cancel').addEventListener('click', onCancel);
            $('#modal-close').addEventListener('click', onCancel);
            if (typeof onRender === 'function') onRender(body);
            dlg.showModal();
            // Accessibility: move focus into the dialog's first field.
            const firstField = body.querySelector('input:not([type=checkbox]), select, textarea, input');
            if (firstField) firstField.focus();
        });
    }

    // custom content modal (no form fields)
    function contentModal(title, buildBody, { closeLabel = 'Schließen' } = {}) {
        const dlg = $('#modal');
        $('#modal-title').textContent = title;
        $('#modal-submit').style.display = 'none';
        $('#modal-cancel').textContent = closeLabel;
        $('#modal-cancel').style.display = '';
        const body = $('#modal-body');
        body.innerHTML = '';
        buildBody(body, () => dlg.close());
        const close = () => { $('#modal-cancel').removeEventListener('click', close); $('#modal-close').removeEventListener('click', close); dlg.close(); };
        $('#modal-cancel').addEventListener('click', close);
        $('#modal-close').addEventListener('click', close);
        dlg.showModal();
        return dlg;
    }

    // ---------- charts (pure SVG) ----------
    const MONTHS = ['J', 'F', 'M', 'A', 'M', 'J', 'J', 'A', 'S', 'O', 'N', 'D'];
    function chartBars(values, labels, cls = 'barc') {
        const W = 340, H = 150, padX = 8, padTop = 12, padBot = 20;
        const n = values.length, max = Math.max(1, ...values);
        const gap = (W - 2 * padX) / n, bw = Math.min(24, gap * 0.6);
        let s = `<line class="axis" x1="${padX}" y1="${H - padBot}" x2="${W - padX}" y2="${H - padBot}"/>`;
        for (let i = 0; i < n; i++) {
            const h = (values[i] / max) * (H - padTop - padBot);
            const x = padX + i * gap + (gap - bw) / 2, y = H - padBot - h;
            s += `<rect class="${cls}" x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(0, h).toFixed(1)}" rx="2"/>`;
            if (labels) s += `<text class="lbl" x="${(x + bw / 2).toFixed(1)}" y="${H - padBot + 12}" text-anchor="middle">${labels[i]}</text>`;
        }
        const box = el('div', { class: 'chart' });
        box.innerHTML = `<svg viewBox="0 0 ${W} ${H}" role="img">${s}</svg>`;
        return box;
    }
    function chartLine(values, labels) {
        const W = 340, H = 150, padX = 10, padTop = 12, padBot = 20;
        const n = values.length, max = Math.max(1, ...values);
        const step = (W - 2 * padX) / (n - 1 || 1);
        const pts = values.map((v, i) => [padX + i * step, H - padBot - (v / max) * (H - padTop - padBot)]);
        const path = pts.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ');
        let dots = '';
        pts.forEach((p, i) => {
            dots += `<circle class="dot" cx="${p[0].toFixed(1)}" cy="${p[1].toFixed(1)}" r="2.5"/>`;
            if (labels) dots += `<text class="lbl" x="${p[0].toFixed(1)}" y="${H - padBot + 12}" text-anchor="middle">${labels[i]}</text>`;
        });
        const box = el('div', { class: 'chart' });
        box.innerHTML = `<svg viewBox="0 0 ${W} ${H}" role="img"><line class="axis" x1="${padX}" y1="${H - padBot}" x2="${W - padX}" y2="${H - padBot}"/><polyline class="linec" points="${path}"/>${dots}</svg>`;
        return box;
    }

    // ---------- shared render helpers ----------
    const emptyState = (icon, text) => el('div', { class: 'empty' }, el('span', { class: 'big' }, icon), text);
    const stat = (value, label) => el('div', { class: 'stat' }, el('div', { class: 'value' }, esc(value)), el('div', { class: 'label' }, label));
    const personName = (p) => (`${p.first_name || ''} ${p.last_name || ''}`).trim() || '(ohne Namen)';
    const catById = (id) => state.categories.find((c) => c.id === Number(id));
    const STATUS_LABEL = { reserved: 'reserviert', stored: 'eingelagert', collected: 'abgeholt', cancelled: 'storniert' };
    const ROLE_LABEL = { admin: 'Administrator', editor: 'Bearbeiter', reader: 'Nur-Lesen' };
    const statusBadge = (s) => el('span', { class: 'badge badge-' + s }, STATUS_LABEL[s] || s);

    async function refreshLookups() {
        const [persons, categories, services] = await Promise.all([
            api.get('/persons'), api.get('/categories'), api.get('/services'),
        ]);
        state.persons = persons; state.categories = categories; state.services = services;
    }

    // ---------- generic list with search / sort / pagination ----------
    function mountList(page, opts) {
        const pageSize = opts.pageSize || 10;
        let q = '', sortIdx = opts.defaultSort || 0, pageNum = 1;

        page.innerHTML = '';
        const head = el('div', { class: 'page-head' }, el('h2', {}, opts.title));
        if (opts.onAdd && opts.canAdd !== false) {
            head.append(el('button', { class: 'btn btn-primary btn-sm', onclick: opts.onAdd }, '+ Neu'));
        }
        page.append(head);

        const search = el('input', { class: 'search', type: 'search', placeholder: 'Suche …', value: q });
        const sortSel = el('select', {}, ...opts.sorts.map((s, i) => el('option', { value: i, selected: i === sortIdx }, s.label)));
        const toolbar = el('div', { class: 'toolbar' }, search, sortSel);
        const controlState = {};
        if (opts.controls) for (const c of opts.controls(() => { pageNum = 1; refresh(); }, controlState)) toolbar.append(c);
        page.append(toolbar);

        const listEl = el('div', {});
        const pagerEl = el('div', {});
        page.append(listEl, pagerEl);

        search.addEventListener('input', () => { q = norm(search.value); pageNum = 1; refresh(); });
        sortSel.addEventListener('change', () => { sortIdx = Number(sortSel.value); refresh(); });

        function refresh() {
            let items = opts.items.slice();
            if (opts.extraFilter) items = items.filter((it) => opts.extraFilter(it, controlState));
            if (q) items = items.filter((it) => opts.searchText(it).includes(q));
            const s = opts.sorts[sortIdx];
            if (s && s.cmp) items.sort(s.cmp);
            const total = items.length;
            const pages = Math.max(1, Math.ceil(total / pageSize));
            if (pageNum > pages) pageNum = pages;
            const start = (pageNum - 1) * pageSize;
            const slice = items.slice(start, start + pageSize);
            listEl.innerHTML = '';
            if (!slice.length) listEl.append(emptyState(opts.emptyIcon || '∅', opts.emptyText || 'Keine Einträge.'));
            else slice.forEach((it) => listEl.append(opts.render(it)));
            pagerEl.innerHTML = '';
            if (total > pageSize) {
                pagerEl.append(el('div', { class: 'pager' },
                    el('button', { class: 'btn btn-ghost btn-sm', disabled: pageNum <= 1, onclick: () => { pageNum--; refresh(); } }, '‹'),
                    el('span', { class: 'info' }, `${pageNum} / ${pages} · ${total}`),
                    el('button', { class: 'btn btn-ghost btn-sm', disabled: pageNum >= pages, onclick: () => { pageNum++; refresh(); } }, '›'),
                ));
            }
        }
        opts.refresh = refresh;
        refresh();
        return opts;
    }

    // ================= ROUTER =================
    const routes = {};
    function parseHash() {
        const raw = (location.hash || '#/dashboard').replace(/^#\/?/, '');
        const [name, id] = raw.split('/');
        return { name: name || 'dashboard', id: id ? Number(id) : null };
    }
    const TAB_FOR = { dashboard: 'dashboard', persons: 'persons', person: 'persons', vehicles: 'vehicles', vehicle: 'vehicles', finance: 'finance', tariffs: 'tariffs' };
    function navigate(path) {
        if (('#/' + path) === location.hash) render();
        else location.hash = '#/' + path;
    }
    // Loading placeholder: a few shimmering rows instead of a bare "Lädt…".
    function skeleton(rows = 4) {
        const wrap = el('div', { class: 'skeleton-wrap', 'aria-hidden': 'true' });
        wrap.append(el('div', { class: 'sk sk-head' }));
        for (let i = 0; i < rows; i++) wrap.append(el('div', { class: 'sk sk-card' }));
        return wrap;
    }
    async function render() {
        const { name, id } = parseHash();
        const routeName = id != null && (name === 'persons' || name === 'vehicles') ? name.slice(0, -1) : name;
        $$('.tab').forEach((t) => {
            const on = t.dataset.route === (TAB_FOR[routeName] || TAB_FOR[name]);
            t.classList.toggle('active', on);
            if (on) t.setAttribute('aria-current', 'page');
            else t.removeAttribute('aria-current');
        });
        const page = $('#page');
        page.innerHTML = '';
        page.append(skeleton());
        const fn = routes[routeName] || routes.dashboard;
        try { await fn(page, id); window.scrollTo(0, 0); }
        catch (err) {
            page.innerHTML = '';
            page.append(el('div', { class: 'empty' }, 'Fehler: ' + err.message));
            if (err.status === 401) logout();
        }
    }

    // ================= DASHBOARD =================
    routes.dashboard = async (page) => {
        const ov = await api.get('/overview');
        page.innerHTML = '';
        page.append(el('div', { class: 'page-head' }, el('h2', {}, 'Übersicht')));
        page.append(el('div', { class: 'stat-grid' },
            stat(ov.total_persons, 'Personen'),
            stat(ov.active_vehicles, 'aktiv eingestellt'),
            stat(ov.total_vehicles, 'Gefährte gesamt'),
            stat(ov.total_categories, 'Tarife'),
        ));
        page.append(el('div', { class: 'stat-grid' },
            statWide(eur(ov.accrued_this_year), 'Umsatz ' + ov.year),
            statWide(eur(ov.paid_total), 'Bezahlt (Slider)'),
            statWide(eur(ov.outstanding_total), 'Offen gesamt'),
        ));

        // Revenue chart
        const revCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Umsatz pro Monat · ' + ov.year));
        revCard.append(chartLine(ov.revenue_by_month, MONTHS));
        revCard.append(el('div', { class: 'legend' }, el('span', {}, el('span', { class: 'dotc', style: 'background:var(--primary)' }), 'Aufgelaufene Miete')));
        page.append(revCard);

        // Extra charges per month
        const pcCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Zusatzkosten pro Monat · ' + ov.year));
        pcCard.append(chartBars(ov.charges_by_month, MONTHS, 'barc'));
        pcCard.append(el('div', { class: 'legend' }, el('span', {}, el('span', { class: 'dotc', style: 'background:var(--primary)' }), 'Zusatzkosten')));
        page.append(pcCard);

        // Status distribution
        const sc = ov.status_counts || {};
        const scCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Gefährte nach Status'));
        const maxS = Math.max(1, ...Object.values(sc));
        const bars = el('div', { class: 'bars' });
        for (const st of ['stored', 'reserved', 'collected', 'cancelled']) {
            bars.append(el('div', { class: 'bar-row' },
                statusBadge(st),
                el('div', { class: 'bar-track' }, el('div', { class: 'bar-fill', style: `width:${((sc[st] || 0) / maxS) * 100}%` })),
                el('div', { class: 'bar-val' }, String(sc[st] || 0))));
        }
        scCard.append(bars);
        page.append(scCard);
    };
    const statWide = (value, label) => el('div', { class: 'stat', style: 'grid-column: span 2;' }, el('div', { class: 'value' }, esc(value)), el('div', { class: 'label' }, label));

    // ================= PERSONS =================
    routes.persons = async (page) => {
        await refreshLookups();
        mountList(page, {
            title: 'Personen', emptyIcon: '☺', emptyText: 'Noch keine Personen.',
            onAdd: canManage() ? () => personForm() : null,
            items: state.persons,
            searchText: (p) => norm(personName(p) + ' ' + p.email + ' ' + p.phone),
            sorts: [
                { label: 'Name A–Z', cmp: (a, b) => personName(a).localeCompare(personName(b)) },
                { label: 'Name Z–A', cmp: (a, b) => personName(b).localeCompare(personName(a)) },
                { label: 'Neueste zuerst', cmp: (a, b) => b.id - a.id },
            ],
            render: (p) => el('div', { class: 'card' },
                el('div', { class: 'card-row' },
                    el('div', { style: 'flex:1;cursor:pointer', onclick: () => navigate('persons/' + p.id) },
                        el('h3', {}, personName(p), ' ', p.has_flat_rate ? el('span', { class: 'badge badge-active', title: 'Pauschale' }, 'Pauschale') : null),
                        el('div', { class: 'card-meta' }, [p.email, p.phone].filter(Boolean).join(' · ') || 'keine Kontaktdaten')),
                    el('div', { class: 'card-actions' },
                        el('button', { class: 'btn btn-ghost btn-sm', onclick: () => navigate('persons/' + p.id) }, '›'),
                        canManage() && el('button', { class: 'btn btn-ghost btn-sm', onclick: () => personForm(p) }, '✎'),
                        canManage() && el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delPerson(p) }, '🗑'),
                    ))),
        });
    };

    async function personForm(existing) {
        const data = await formModal({
            title: existing ? 'Person bearbeiten' : 'Neue Person',
            fields: [
                { name: 'first_name', label: 'Vorname', value: existing?.first_name },
                { name: 'last_name', label: 'Nachname', value: existing?.last_name },
                { name: 'email', label: 'E-Mail', type: 'email', value: existing?.email },
                { name: 'phone', label: 'Telefon', value: existing?.phone },
                { name: 'address', label: 'Adresse', type: 'textarea', value: existing?.address },
                { name: 'notes', label: 'Notizen', type: 'textarea', value: existing?.notes },
            ],
        });
        if (!data) return;
        try {
            if (existing) await api.put('/persons/' + existing.id, data);
            else await api.post('/persons', data);
            toast('Person gespeichert', 'success'); render();
        } catch (e) { toast(e.message, 'error'); }
    }
    function delPerson(p) {
        deleteWithUndo('Person löschen?', `„${personName(p)}“ und alle zugehörigen Gefährte werden gelöscht.`,
            () => api.del('/persons/' + p.id), () => render());
    }

    // ---------- PERSON DETAIL ----------
    routes.person = async (page, id) => {
        await refreshLookups();
        const stats = await api.get('/persons/' + id + '/stats');
        const [vehicles, charges] = await Promise.all([
            api.get('/vehicles?person_id=' + id), api.get('/charges?person_id=' + id),
        ]);
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', onclick: () => navigate('persons'), 'aria-label': 'Zurück' }, '‹'),
            el('h2', { style: 'margin:0' }, stats.person_name || 'Person')));

        // balance card
        const balCls = stats.balance > 0.005 ? 'amt-pos' : 'amt-zero';
        page.append(el('div', { class: 'card' },
            el('div', { class: 'balance' }, el('span', {}, 'Aufgelaufene Miete'), el('span', { class: 'amt' }, eur(stats.total_accrued))),
            el('div', { class: 'balance' }, el('span', {}, 'Zusatzkosten'), el('span', { class: 'amt' }, eur(stats.total_charges))),
            el('div', { class: 'balance' }, el('span', {}, 'Bezahlt (per Slider)'), el('span', { class: 'amt' }, '− ' + eur(stats.total_paid))),
            el('div', { class: 'balance' }, el('strong', {}, 'Offener Saldo'), el('strong', { class: 'amt ' + balCls }, eur(stats.balance)))));

        // flat-rate agreements (Pauschalen)
        const ags = stats.agreements || [];
        const coveredVids = new Set();
        let coversAll = false;
        for (const a of ags) {
            if (!a.vehicle_ids || !a.vehicle_ids.length) coversAll = true;
            else a.vehicle_ids.forEach((vid) => coveredVids.add(vid));
        }
        if (canBill() || ags.length) {
            const frCard = el('div', { class: 'card' });
            frCard.append(el('div', { class: 'card-row' },
                el('h3', { style: 'margin:0' }, 'Pauschalen'),
                canBill() ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => agreementForm(id, vehicles) }, '+ Pauschale') : null));
            if (!ags.length) frCard.append(el('div', { class: 'card-meta', style: 'margin-top:.3rem' }, 'Keine Pauschale – Abrechnung je Gefährt.'));
            else ags.forEach((a) => frCard.append(agreementRow(id, a, vehicles)));
            page.append(frCard);
        }

        // monthly chart
        const chartCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Aufgelaufene Miete pro Monat · ' + stats.year));
        chartCard.append(chartBars(stats.monthly_accrued, MONTHS));
        page.append(chartCard);

        // years (informational, combined rent per year)
        if (stats.years.length) {
            const yc = el('div', { class: 'chart-card' }, el('h3', {}, 'Kosten pro Jahr'));
            const max = Math.max(...stats.years.map((y) => y.cost), 1);
            const bars = el('div', { class: 'bars' });
            for (const y of stats.years) bars.append(el('div', { class: 'bar-row' },
                el('div', {}, String(y.year)),
                el('div', { class: 'bar-track' }, el('div', { class: 'bar-fill', style: `width:${(y.cost / max) * 100}%` })),
                el('div', { class: 'bar-val' }, eur(y.cost))));
            yc.append(bars);
            page.append(yc);
        }

        // vehicles
        const vh = el('div', { class: 'page-head' }, el('h3', {}, 'Gefährte (' + vehicles.length + ')'));
        if (canManage()) vh.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => vehicleForm(null, id) }, '+ Gefährt'));
        page.append(vh);
        if (!vehicles.length) page.append(el('p', { class: 'muted' }, 'Keine Gefährte.'));
        else vehicles.forEach((v) => page.append(vehicleCard(v, { linkable: true, covered: coversAll || coveredVids.has(v.id) })));

        // charges
        const ch = el('div', { class: 'page-head' }, el('h3', {}, 'Zusatzkosten'));
        if (canBill()) ch.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => chargeForm(id) }, '+ Position'));
        page.append(ch);
        page.append(financeList(charges));
    };

    // ================= VEHICLES =================
    routes.vehicles = async (page) => {
        await refreshLookups();
        const vehicles = await api.get('/vehicles');
        mountList(page, {
            title: 'Gefährte', emptyIcon: '▣', emptyText: 'Keine Gefährte in dieser Ansicht.',
            onAdd: canManage() ? () => vehicleForm() : null,
            items: vehicles,
            searchText: (v) => norm([v.label, v.license_plate, v.category_name, v.person_name].join(' ')),
            sorts: [
                { label: 'Neueste zuerst', cmp: (a, b) => new Date(b.start_date) - new Date(a.start_date) },
                { label: 'Bezeichnung', cmp: (a, b) => norm(a.label || a.license_plate).localeCompare(norm(b.label || b.license_plate)) },
                { label: 'Kosten absteigend', cmp: (a, b) => b.accrued_cost - a.accrued_cost },
            ],
            controls: (refresh, cs) => {
                cs.status = ''; cs.person = '';
                const stSel = el('select', {}, el('option', { value: '' }, 'Alle Status'),
                    ...['stored', 'reserved', 'collected', 'cancelled'].map((s) => el('option', { value: s }, STATUS_LABEL[s])));
                stSel.addEventListener('change', () => { cs.status = stSel.value; refresh(); });
                const peSel = el('select', {}, el('option', { value: '' }, 'Alle Personen'),
                    ...state.persons.map((p) => el('option', { value: p.id }, personName(p))));
                peSel.addEventListener('change', () => { cs.person = peSel.value; refresh(); });
                return [stSel, peSel];
            },
            extraFilter: (v, cs) => (!cs.status || v.status === cs.status) && (!cs.person || String(v.person_id) === cs.person),
            render: (v) => vehicleCard(v, { linkable: true }),
        });
    };

    function vehicleCard(v, { linkable = true, covered = false } = {}) {
        const title = v.label || v.license_plate || v.category_name;
        const rateUnit = v.billing_period === 'yearly' ? '/Jahr' : '/Monat';
        const main = el('div', { style: 'flex:1;' + (linkable ? 'cursor:pointer' : ''), onclick: linkable ? () => navigate('vehicles/' + v.id) : null },
            el('h3', {}, esc(title)),
            el('div', { class: 'card-meta' }, el('span', { class: 'badge badge-cat' }, esc(v.category_name)), ' ', esc(v.person_name),
                v.photo_count ? el('span', { class: 'muted' }, '  📷 ' + v.photo_count) : null),
            el('div', { class: 'card-meta' }, `${eur(v.effective_rate)}${rateUnit}` + (v.cost_override != null ? ' (Sonderpreis)' : '') +
                ` · seit ${fmtDate(v.start_date)}` + (v.end_date ? ` bis ${fmtDate(v.end_date)}` : '')),
            el('div', { class: 'card-meta', style: 'color:var(--text);font-weight:600;margin-top:.3rem' }, 'Aufgelaufen: ' + eur(v.accrued_cost)));
        const actions = el('div', { class: 'card-actions' },
            canManage() && el('button', { class: 'btn btn-ghost btn-sm', onclick: () => vehicleForm(v) }, '✎'),
            canManage() && el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delVehicle(v) }, '🗑'));
        return el('div', { class: 'card' },
            el('div', { class: 'card-row' }, main, actions),
            vehicleControls(v, covered));
    }

    // Slider-based quick controls: status + payment, shown on card and detail.
    // When the vehicle is covered by a flat-rate agreement an "in Pauschale" hint
    // is shown; the payment slider still marks the uncovered portion as paid.
    function vehicleControls(v, covered = false) {
        const wrap = el('div', { class: 'controls-row' });
        wrap.append(statusSlider(v));
        if (canBill()) wrap.append(paidSlider(v));
        if (covered) wrap.append(el('span', { class: 'badge badge-cat', title: 'Zeitweise über eine Pauschale abgerechnet' }, 'in Pauschale'));
        if (canManage() && v.status === 'collected') {
            wrap.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => duplicateVehicle(v) }, '↻ Erneut einstellen'));
        }
        return wrap;
    }

    // ---- flat-rate agreements (Pauschale-Einträge) ----
    function agreementRow(personId, a, vehicles) {
        const unit = a.period === 'yearly' ? '/Jahr' : '/Monat';
        const covered = (a.vehicle_ids && a.vehicle_ids.length)
            ? a.vehicle_ids.map((vid) => { const v = vehicles.find((x) => x.id === vid); return v ? (v.label || v.license_plate || v.category_name) : '#' + vid; }).join(', ')
            : 'alle Gefährte';
        const row = el('div', { class: 'card', style: 'margin-top:.5rem' });
        row.append(el('div', { class: 'card-row' },
            el('div', { style: 'flex:1' },
                el('div', {}, el('strong', {}, eur(a.amount) + unit)),
                el('div', { class: 'card-meta' }, fmtDate(a.start_date) + (a.end_date ? ' – ' + fmtDate(a.end_date) : ' – offen') + ' · ' + esc(covered)),
                el('div', { class: 'card-meta' }, 'aufgelaufen ' + eur(a.accrued) + (a.note ? ' · ' + esc(a.note) : ''))),
            canBill() ? el('div', { class: 'card-actions' },
                el('button', { class: 'btn btn-ghost btn-sm', onclick: () => agreementForm(personId, vehicles, a) }, '✎'),
                el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delAgreement(a) }, '🗑')) : null));
        if (canBill()) row.append(el('div', { class: 'controls-row' }, agreementPaidSlider(a)));
        else row.append(el('span', { class: 'badge ' + (a.paid ? 'badge-active' : 'badge-ended') }, a.paid ? 'bezahlt' : 'offen'));
        return row;
    }
    function agreementPaidSlider(a) {
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus Pauschale' });
        const setPaid = async (val, e) => {
            markActive(e.currentTarget);
            try { await api.post('/agreements/' + a.id + '/paid', { paid: val }); toast(val ? 'bezahlt' : 'offen', 'success'); render(); }
            catch (err) { toast(err.message, 'error'); render(); }
        };
        seg.append(el('button', { class: (!a.paid ? 'active open' : ''), type: 'button', role: 'radio', 'aria-checked': String(!a.paid), onclick: (e) => setPaid(false, e) }, 'offen'));
        seg.append(el('button', { class: (a.paid ? 'active done' : ''), type: 'button', role: 'radio', 'aria-checked': String(a.paid), onclick: (e) => setPaid(true, e) }, 'bezahlt'));
        return seg;
    }
    async function delAgreement(a) {
        if (!await confirmDialog('Pauschale löschen?', 'Der Pauschaleintrag wird entfernt.', 'Löschen')) return;
        try { await api.del('/agreements/' + a.id); toast('Pauschale gelöscht', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    async function agreementForm(personId, vehicles, existing) {
        const selected = new Set((existing && existing.vehicle_ids) || []);
        const data = await formModal({
            title: existing ? 'Pauschale bearbeiten' : 'Neue Pauschale',
            submitLabel: 'Speichern',
            fields: [
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', min: 0, required: true, value: existing?.amount ?? '' },
                { name: 'period', label: 'Zeitraum', type: 'select', value: existing?.period || 'monthly', options: [{ value: 'monthly', label: 'pro Monat' }, { value: 'yearly', label: 'pro Jahr' }] },
                { name: 'start_date', label: 'Gültig ab', type: 'date', required: true, value: existing?.start_date ? existing.start_date.slice(0, 10) : today() },
                { name: 'end_date', label: 'Gültig bis (optional)', type: 'date', value: existing?.end_date ? existing.end_date.slice(0, 10) : '', help: 'Leer = laufend.' },
                { name: 'note', label: 'Notiz (optional)', value: existing?.note },
            ],
            onRender: (body) => {
                if (!vehicles.length) return;
                body.append(el('label', {}, 'Gefährte (keine Auswahl = alle)'));
                const box = el('div', { class: 'agreement-vehicles' });
                for (const v of vehicles) {
                    const cb = el('input', { type: 'checkbox' });
                    if (selected.has(v.id)) cb.checked = true;
                    cb.addEventListener('change', () => { cb.checked ? selected.add(v.id) : selected.delete(v.id); });
                    box.append(el('label', { class: 'switch' }, cb, el('span', { class: 'track' }), el('span', {}, esc(v.label || v.license_plate || v.category_name))));
                }
                body.append(box);
            },
        });
        if (!data) return;
        const payload = {
            amount: data.amount === '' ? null : Number(data.amount),
            period: data.period,
            start_date: data.start_date,
            end_date: data.end_date === '' ? null : data.end_date,
            note: data.note,
            vehicle_ids: [...selected],
        };
        try {
            if (existing) await api.put('/agreements/' + existing.id, payload);
            else await api.post('/persons/' + personId + '/agreements', payload);
            toast('Pauschale gespeichert', 'success'); render();
        } catch (e) { toast(e.message, 'error'); }
    }

    const STATUS_FLOW = ['reserved', 'stored', 'collected'];
    function statusSlider(v) {
        if (!canManage()) return statusBadge(v.status);
        const seg = el('div', { class: 'seg-mini', role: 'radiogroup', 'aria-label': 'Lagerstatus' });
        for (const s of STATUS_FLOW) {
            const on = v.status === s;
            seg.append(el('button', {
                class: 'st-' + s + (on ? ' active' : ''), type: 'button',
                role: 'radio', 'aria-checked': String(on), 'aria-label': 'Status: ' + STATUS_LABEL[s],
                onclick: (e) => { if (v.status !== s) { markActive(e.currentTarget); changeStatus(v, s, { silent: true }); } },
            }, STATUS_LABEL[s]));
        }
        if (v.status === 'cancelled') {
            seg.prepend(el('button', { class: 'st-cancelled active', type: 'button', role: 'radio', 'aria-checked': 'true', disabled: true }, STATUS_LABEL.cancelled));
        }
        return seg;
    }
    function paidSlider(v) {
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus' });
        seg.append(el('button', { class: (!v.paid ? 'active open' : ''), type: 'button', role: 'radio', 'aria-checked': String(!v.paid), 'aria-label': 'Zahlung offen',
            onclick: (e) => { markActive(e.currentTarget); markPaid(v, false); } }, 'offen'));
        seg.append(el('button', { class: (v.paid ? 'active done' : ''), type: 'button', role: 'radio', 'aria-checked': String(v.paid), 'aria-label': 'Bezahlt',
            onclick: (e) => { markActive(e.currentTarget); markPaid(v, true); } }, 'bezahlt'));
        return seg;
    }
    // Optimistic feedback: instantly reflect the picked segment before the API returns.
    function markActive(btn) {
        for (const sib of btn.parentElement.children) {
            sib.classList.toggle('active', sib === btn);
            if (sib.hasAttribute('aria-checked')) sib.setAttribute('aria-checked', String(sib === btn));
        }
    }
    async function markPaid(v, paid) {
        if (v.paid === paid) return;
        try { await api.post('/vehicles/' + v.id + '/paid', { paid }); toast(paid ? 'Als bezahlt markiert' : 'Als offen markiert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); render(); }
    }
    async function duplicateVehicle(v) {
        const d = await formModal({
            title: 'Erneut einstellen',
            submitLabel: 'Einstellen',
            fields: [{ name: 'start_date', label: 'Neues Einstelldatum', type: 'date', required: true, value: today(),
                help: 'Typ, Kennzeichen, Preis und Fotos werden übernommen.' }],
        });
        if (!d) return;
        try {
            const nv = await api.post('/vehicles/' + v.id + '/duplicate', { start_date: d.start_date });
            toast('Erneut eingestellt', 'success');
            navigate('vehicles/' + nv.id);
        } catch (e) { toast(e.message, 'error'); }
    }

    async function vehicleForm(existing, presetPerson) {
        if (!state.persons.length) { toast('Zuerst eine Person anlegen', 'error'); return; }
        if (!state.categories.length) { toast('Zuerst einen Tarif anlegen', 'error'); return; }
        const data = await formModal({
            title: existing ? 'Gefährt bearbeiten' : 'Neues Gefährt',
            fields: [
                { name: 'person_id', label: 'Person', type: 'select', required: true, value: existing?.person_id ?? presetPerson ?? state.persons[0].id, options: state.persons.map((p) => ({ value: p.id, label: personName(p) })) },
                { name: 'category_id', label: 'Tarif / Typ', type: 'select', required: true, value: existing?.category_id ?? state.categories[0].id, options: state.categories.map((c) => ({ value: c.id, label: `${c.name} (${eur(c.default_monthly_cost)}/M, ${eur(c.default_yearly_cost)}/J)` })) },
                { name: 'label', label: 'Bezeichnung', value: existing?.label, placeholder: 'z. B. Wohnwagen Hobby' },
                { name: 'license_plate', label: 'Kennzeichen', value: existing?.license_plate },
                { name: 'start_date', label: 'Einstelldatum', type: 'date', required: true, value: existing?.start_date ? existing.start_date.slice(0, 10) : today() },
                { name: 'end_date', label: 'Abholdatum (optional)', type: 'date', value: existing?.end_date ? existing.end_date.slice(0, 10) : '', help: 'Leer = noch eingelagert. Hier auch nachträglich änderbar.' },
                { name: 'billing_period', label: 'Abrechnung', type: 'select', value: existing?.billing_period || 'monthly', options: [{ value: 'monthly', label: 'monatlich' }, { value: 'yearly', label: 'jährlich' }] },
                { name: 'cost_override', label: 'Sonderpreis (optional)', type: 'number', step: '0.01', min: 0, value: existing?.cost_override ?? '', help: 'Leer = zentraler Tarifpreis. Status & Abholung stellst du danach mit den Schiebereglern ein.' },
                { name: 'notes', label: 'Notizen', type: 'textarea', value: existing?.notes },
            ],
        });
        if (!data) return;
        const payload = {
            person_id: Number(data.person_id), category_id: Number(data.category_id),
            label: data.label, license_plate: data.license_plate, notes: data.notes, billing_period: data.billing_period,
            cost_override: data.cost_override === '' ? null : Number(data.cost_override),
            start_date: data.start_date,
            // Abholdatum (end_date) is editable here so it can be corrected
            // retroactively; status/reservation stay slider-managed.
            status: existing?.status || 'stored',
            end_date: data.end_date === '' ? null : data.end_date,
            reserved_from: existing?.reserved_from ? existing.reserved_from.slice(0, 10) : null,
            reserved_until: existing?.reserved_until ? existing.reserved_until.slice(0, 10) : null,
        };
        try {
            if (existing) await api.put('/vehicles/' + existing.id, payload);
            else await api.post('/vehicles', payload);
            toast('Gefährt gespeichert', 'success'); render();
        } catch (e) { toast(e.message, 'error'); }
    }
    function delVehicle(v) {
        deleteWithUndo('Gefährt löschen?', `„${v.label || v.category_name}“ wird gelöscht.`,
            () => api.del('/vehicles/' + v.id), () => render());
    }

    // ---------- VEHICLE DETAIL ----------
    routes.vehicle = async (page, id) => {
        await refreshLookups();
        const [vehicles, photos, history] = await Promise.all([
            api.get('/vehicles'), api.get('/vehicles/' + id + '/photos'), api.get('/vehicles/' + id + '/history'),
        ]);
        const v = vehicles.find((x) => x.id === id);
        if (!v) { page.innerHTML = ''; page.append(emptyState('▣', 'Gefährt nicht gefunden.')); return; }
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', onclick: () => navigate('vehicles') }, '‹'),
            el('h2', { style: 'margin:0;flex:1' }, esc(v.label || v.license_plate || v.category_name)), statusBadge(v.status)));

        page.append(el('div', { class: 'card' },
            el('div', { class: 'balance' }, el('span', {}, 'Person'), el('span', {}, el('a', { href: '#/persons/' + v.person_id }, esc(v.person_name)))),
            el('div', { class: 'balance' }, el('span', {}, 'Typ / Tarif'), el('span', {}, esc(v.category_name))),
            el('div', { class: 'balance' }, el('span', {}, 'Kennzeichen'), el('span', {}, esc(v.license_plate || '–'))),
            el('div', { class: 'balance' }, el('span', {}, 'Preis'), el('span', { class: 'amt' }, eur(v.effective_rate) + (v.billing_period === 'yearly' ? ' /Jahr' : ' /Monat') + (v.cost_override != null ? ' (Sonderpreis)' : ''))),
            el('div', { class: 'balance' }, el('span', {}, 'Zeitraum'), el('span', {}, fmtDate(v.start_date) + (v.end_date ? ' – ' + fmtDate(v.end_date) : ' – offen'))),
            v.reserved_from ? el('div', { class: 'balance' }, el('span', {}, 'Reservierung'), el('span', {}, fmtDate(v.reserved_from) + ' – ' + fmtDate(v.reserved_until))) : null,
            el('div', { class: 'balance' }, el('strong', {}, 'Aufgelaufen'), el('strong', { class: 'amt' }, eur(v.accrued_cost))),
            v.notes ? el('div', { class: 'card-meta', style: 'margin-top:.5rem' }, esc(v.notes)) : null));

        // status & payment sliders
        if (canManage() || canBill()) {
            const ctrl = el('div', { class: 'card' }, el('h3', {}, 'Status & Zahlung'), vehicleControls(v));
            if (canManage() && v.status !== 'cancelled') {
                ctrl.append(el('button', { class: 'btn btn-ghost btn-sm', style: 'margin-top:.7rem', onclick: () => changeStatus(v, 'cancelled') }, '✕ Stornieren'));
            }
            page.append(ctrl);
        }
        if (canManage()) {
            page.append(el('div', { class: 'page-head' }, el('h3', {}, 'Bearbeiten'),
                el('button', { class: 'btn btn-ghost btn-sm', onclick: () => vehicleForm(v) }, '✎ Bearbeiten')));
        }

        // photos
        const photoCard = el('div', { class: 'card' });
        const ph = el('div', { class: 'page-head' }, el('h3', {}, 'Fotos'));
        if (canManage()) {
            const fileInput = el('input', { type: 'file', accept: 'image/jpeg,image/png', style: 'display:none' });
            fileInput.addEventListener('change', () => uploadPhoto(id, fileInput.files[0]));
            const btn = el('button', { class: 'btn btn-primary btn-sm', onclick: () => fileInput.click() }, '+ Foto');
            ph.append(btn, fileInput);
        }
        photoCard.append(ph);
        if (!photos.length) photoCard.append(el('p', { class: 'muted' }, 'Keine Fotos.'));
        else {
            const grid = el('div', { class: 'photo-grid' });
            for (const p of photos) {
                const img = el('img', { src: '/api/photos/' + p.id, alt: esc(p.filename), loading: 'lazy', onclick: () => lightbox(p.id) });
                const thumb = el('div', { class: 'photo-thumb' }, img);
                if (canManage()) thumb.append(el('button', { class: 'del', title: 'Löschen', onclick: () => delPhoto(p, id) }, '✕'));
                grid.append(thumb);
            }
            photoCard.append(grid);
        }
        page.append(photoCard);

        // history timeline
        if (history.length) {
            const hc = el('div', { class: 'card' }, el('h3', {}, 'Verlauf'));
            const ul = el('ul', { class: 'timeline' });
            for (const h of history) {
                ul.append(el('li', {},
                    el('div', {}, (h.old_status ? STATUS_LABEL[h.old_status] + ' → ' : '') + (STATUS_LABEL[h.new_status] || h.new_status) + (h.note ? ' — ' + esc(h.note) : '')),
                    el('div', { class: 't-time' }, fmtDateTime(h.created_at) + (h.changed_by ? ' · ' + esc(h.changed_by) : ''))));
            }
            hc.append(ul); page.append(hc);
        }
    };

    async function changeStatus(v, s, opts = {}) {
        let note = '', date;
        if (s === 'collected') {
            // Always ask for the pickup date (default today, editable) so older
            // pickups can be back-dated instead of always using the current date.
            const d = await formModal({
                title: STATUS_LABEL[s], submitLabel: 'Bestätigen', fields: [
                    { name: 'date', label: 'Abholdatum', type: 'date', value: (v.end_date || today()).slice(0, 10) },
                    { name: 'note', label: 'Notiz (optional)', type: 'textarea' },
                ],
            });
            if (!d) { render(); return; } // reset optimistic slider state
            note = d.note; date = d.date;
        } else if (!opts.silent && s === 'cancelled') {
            const d = await formModal({ title: STATUS_LABEL[s], submitLabel: 'Bestätigen', fields: [{ name: 'note', label: 'Notiz (optional)', type: 'textarea' }] });
            if (!d) { render(); return; } note = d.note;
        }
        try {
            await api.post('/vehicles/' + v.id + '/status', { status: s, note, date });
            toast('Status: ' + STATUS_LABEL[s], 'success');
            render();
        } catch (e) { toast(e.message, 'error'); }
    }
    async function uploadPhoto(vehicleId, file) {
        if (!file) return;
        const fd = new FormData(); fd.append('photo', file);
        try { await api.upload('/vehicles/' + vehicleId + '/photos', fd); toast('Foto hochgeladen', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delPhoto(p, vehicleId) {
        deleteWithUndo('Foto löschen?', 'Das Foto wird entfernt.', () => api.del('/photos/' + p.id), () => render());
        void vehicleId;
    }
    function lightbox(photoId) {
        const dlg = el('dialog', { class: 'lightbox' }, el('img', { src: '/api/photos/' + photoId, alt: '' }));
        dlg.addEventListener('click', () => dlg.close());
        dlg.addEventListener('close', () => dlg.remove());
        document.body.append(dlg); dlg.showModal();
    }

    // ================= EXTRA CHARGES =================
    routes.finance = async (page) => {
        await refreshLookups();
        const charges = await api.get('/charges');
        mountList(page, {
            title: 'Zusatzkosten', emptyIcon: '€', emptyText: 'Keine Zusatzkosten erfasst.',
            onAdd: canBill() ? () => chargeForm() : null,
            items: charges,
            searchText: (c) => norm([c.person_name, c.description].join(' ')),
            sorts: [{ label: 'Neueste zuerst', cmp: (a, b) => new Date(b.charged_on) - new Date(a.charged_on) }, { label: 'Betrag', cmp: (a, b) => b.total - a.total }],
            render: (c) => financeRow(c),
        });
    };

    function financeList(items) {
        if (!items.length) return el('p', { class: 'muted' }, 'Keine Positionen.');
        const wrap = el('div', {});
        items.forEach((it) => wrap.append(financeRow(it)));
        return wrap;
    }
    function financeRow(it) {
        return el('div', { class: 'card' }, el('div', { class: 'card-row' },
            el('div', { style: 'flex:1' },
                el('h3', {}, eur(it.total) + '  ', el('span', { class: 'muted', style: 'font-weight:400;font-size:.9rem' }, esc(it.description))),
                el('div', { class: 'card-meta' }, esc(it.person_name) + ' · ' + fmtDate(it.charged_on) + (it.quantity !== 1 ? ` · ${it.quantity}×${eur(it.amount)}` : '') + (it.note ? ' · ' + esc(it.note) : ''))),
            canBill() && el('div', { class: 'card-actions' },
                el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delFinance(it) }, '🗑'))));
    }
    function delFinance(it) {
        deleteWithUndo('Position löschen?', 'Der Eintrag wird entfernt.',
            () => api.del('/charges/' + it.id), () => render());
    }

    async function chargeForm(presetPerson) {
        if (!state.persons.length) { toast('Zuerst eine Person anlegen', 'error'); return; }
        const svcOptions = [{ value: '', label: '— frei —' }, ...state.services.map((s) => ({ value: s.name + '|' + s.default_amount, label: `${s.name} (${eur(s.default_amount)})` }))];
        const data = await formModal({
            title: 'Zusatzkosten hinzufügen',
            fields: [
                { name: 'person_id', label: 'Person', type: 'select', required: true, value: presetPerson ?? state.persons[0].id, options: state.persons.map((p) => ({ value: p.id, label: personName(p) })) },
                { name: 'service', label: 'Aus Katalog', type: 'select', value: '', options: svcOptions, help: 'Optional – füllt Bezeichnung & Betrag vor.' },
                { name: 'description', label: 'Bezeichnung', required: true, value: '' },
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', required: true, value: '' },
                { name: 'quantity', label: 'Menge', type: 'number', step: '0.5', value: '1' },
                { name: 'charged_on', label: 'Datum', type: 'date', value: today() },
            ],
        });
        if (!data) return;
        // catalog pre-fill applied client-side if user picked one but left fields empty
        if (data.service) {
            const [nm, amt] = data.service.split('|');
            if (!data.description) data.description = nm;
            if (!data.amount) data.amount = amt;
        }
        try {
            await api.post('/charges', { person_id: Number(data.person_id), description: data.description, amount: Number(data.amount), quantity: Number(data.quantity) || 1, charged_on: data.charged_on });
            toast('Position gespeichert', 'success'); render();
        } catch (e) { toast(e.message, 'error'); }
    }

    // ================= TARIFFS (categories + services) =================
    let tariffTab = 'categories';
    routes.tariffs = async (page) => {
        await refreshLookups();
        page.innerHTML = '';
        page.append(el('div', { class: 'page-head' }, el('h2', {}, 'Tarife & Dienste')));
        const seg = el('div', { class: 'segments' },
            el('button', { class: tariffTab === 'categories' ? 'active' : '', onclick: () => { tariffTab = 'categories'; render(); } }, 'Tarife'),
            el('button', { class: tariffTab === 'services' ? 'active' : '', onclick: () => { tariffTab = 'services'; render(); } }, 'Dienste'));
        page.append(seg);

        if (tariffTab === 'categories') {
            if (isAdmin()) page.append(el('div', { style: 'text-align:right;margin-bottom:.5rem' }, el('button', { class: 'btn btn-primary btn-sm', onclick: () => categoryForm() }, '+ Tarif')));
            page.append(el('p', { class: 'muted', style: 'margin-top:-.3rem' }, 'Zentrale Gefährt-Typen mit Standardpreisen (beim Gefährt überschreibbar).'));
            for (const c of state.categories) {
                page.append(el('div', { class: 'card' }, el('div', { class: 'card-row' },
                    el('div', {}, el('h3', {}, esc(c.name), ' ', c.rates_synced ? el('span', { class: 'badge badge-cat', title: 'Jahr = Monat × 12' }, '×12') : null), el('div', { class: 'card-meta' }, `${eur(c.default_monthly_cost)} / Monat · ${eur(c.default_yearly_cost)} / Jahr`)),
                    isAdmin() && el('div', { class: 'card-actions' },
                        el('button', { class: 'btn btn-ghost btn-sm', onclick: () => categoryForm(c) }, '✎'),
                        el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delCategory(c) }, '🗑')))));
            }
        } else {
            if (isAdmin()) page.append(el('div', { style: 'text-align:right;margin-bottom:.5rem' }, el('button', { class: 'btn btn-primary btn-sm', onclick: () => serviceForm() }, '+ Dienst')));
            page.append(el('p', { class: 'muted', style: 'margin-top:-.3rem' }, 'Katalog für Zusatzleistungen (Strom, Reinigung …).'));
            for (const s of state.services) {
                page.append(el('div', { class: 'card' }, el('div', { class: 'card-row' },
                    el('div', {}, el('h3', {}, esc(s.name)), el('div', { class: 'card-meta' }, eur(s.default_amount))),
                    isAdmin() && el('div', { class: 'card-actions' },
                        el('button', { class: 'btn btn-ghost btn-sm', onclick: () => serviceForm(s) }, '✎'),
                        el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delService(s) }, '🗑')))));
            }
        }
    };
    async function categoryForm(existing) {
        const data = await formModal({
            title: existing ? 'Tarif bearbeiten' : 'Neuer Tarif',
            fields: [
                { name: 'name', label: 'Name', required: true, value: existing?.name },
                { name: 'rates_synced', label: 'Monats-/Jahrespreis koppeln (Jahr = Monat × 12)', type: 'checkbox', value: existing?.rates_synced },
                { name: 'default_monthly_cost', label: 'Preis / Monat (€)', type: 'number', step: '0.01', min: 0, value: existing?.default_monthly_cost ?? 0 },
                { name: 'default_yearly_cost', label: 'Preis / Jahr (€)', type: 'number', step: '0.01', min: 0, value: existing?.default_yearly_cost ?? 0 },
            ],
            // When "koppeln" is checked, editing one price auto-fills the other.
            onRender: (body) => {
                const sync = body.querySelector('#f_rates_synced');
                const m = body.querySelector('#f_default_monthly_cost');
                const y = body.querySelector('#f_default_yearly_cost');
                const r2 = (n) => Math.round(n * 100) / 100;
                const fromMonthly = () => { if (sync.checked) y.value = r2((parseFloat(m.value) || 0) * 12); };
                const fromYearly = () => { if (sync.checked) m.value = r2((parseFloat(y.value) || 0) / 12); };
                m.addEventListener('input', fromMonthly);
                y.addEventListener('input', fromYearly);
                sync.addEventListener('change', fromMonthly);
            },
        });
        if (!data) return;
        const payload = { name: data.name, default_monthly_cost: Number(data.default_monthly_cost), default_yearly_cost: Number(data.default_yearly_cost), rates_synced: data.rates_synced };
        try { existing ? await api.put('/categories/' + existing.id, payload) : await api.post('/categories', payload); toast('Tarif gespeichert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delCategory(c) { deleteWithUndo('Tarif löschen?', `„${c.name}“ wird gelöscht.`, () => api.del('/categories/' + c.id), () => render()); }
    async function serviceForm(existing) {
        const data = await formModal({
            title: existing ? 'Dienst bearbeiten' : 'Neuer Dienst',
            fields: [
                { name: 'name', label: 'Name', required: true, value: existing?.name },
                { name: 'default_amount', label: 'Standardpreis (€)', type: 'number', step: '0.01', min: 0, value: existing?.default_amount ?? 0 },
            ],
        });
        if (!data) return;
        const payload = { name: data.name, default_amount: Number(data.default_amount) };
        try { existing ? await api.put('/services/' + existing.id, payload) : await api.post('/services', payload); toast('Dienst gespeichert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delService(s) { deleteWithUndo('Dienst löschen?', `„${s.name}“ wird gelöscht.`, () => api.del('/services/' + s.id), () => render()); }

    // ================= USERS (admin) =================
    routes.users = async (page) => {
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('⚙', 'Nur für Administratoren.')); return; }
        const users = await api.get('/users');
        mountList(page, {
            title: 'Benutzer', emptyIcon: '⚙', emptyText: 'Keine Benutzer.',
            onAdd: () => userForm(), items: users,
            searchText: (u) => norm([u.username, u.email, ROLE_LABEL[u.role]].join(' ')),
            sorts: [{ label: 'Name A–Z', cmp: (a, b) => a.username.localeCompare(b.username) }],
            render: (u) => el('div', { class: 'card' }, el('div', { class: 'card-row' },
                el('div', {}, el('h3', {}, esc(u.username), ' ', el('span', { class: 'badge badge-role' }, ROLE_LABEL[u.role] || u.role),
                    u.totp_enabled ? el('span', { class: 'badge badge-stored', title: '2FA aktiv' }, '🔐') : null),
                    el('div', { class: 'card-meta' }, esc(u.email) || 'keine E-Mail')),
                el('div', { class: 'card-actions' },
                    u.totp_enabled ? el('button', { class: 'btn btn-ghost btn-sm', title: '2FA zurücksetzen', onclick: () => resetUserMfa(u) }, '🔓') : null,
                    el('button', { class: 'btn btn-ghost btn-sm', onclick: () => userForm(u) }, '✎'),
                    u.id === state.user.id ? null : el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delUser(u) }, '🗑')))),
        });
    };
    async function resetUserMfa(u) {
        if (!await confirmDialog('2FA zurücksetzen?', `Für „${u.username}" wird die Zwei-Faktor-Authentifizierung deaktiviert und alle Recovery-Codes gelöscht. Der Benutzer kann sich dann ohne 2FA anmelden und es neu einrichten.`, 'Zurücksetzen')) return;
        try { await api.post('/users/' + u.id + '/reset-2fa'); toast('2FA zurückgesetzt', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    async function userForm(existing) {
        const data = await formModal({
            title: existing ? 'Benutzer bearbeiten' : 'Neuer Benutzer',
            fields: [
                { name: 'username', label: 'Benutzername', required: !existing, value: existing?.username },
                { name: 'email', label: 'E-Mail', type: 'email', value: existing?.email },
                { name: 'password', label: existing ? 'Neues Passwort (optional)' : 'Passwort', type: 'password', required: !existing, minLength: 8, help: 'Mindestens 8 Zeichen.' },
                { name: 'role', label: 'Rolle', type: 'select', value: existing?.role || 'editor', options: Object.entries(ROLE_LABEL).map(([v, l]) => ({ value: v, label: l })) },
            ],
        });
        if (!data) return;
        const payload = { username: data.username, email: data.email, role: data.role, password: data.password };
        try { existing ? await api.put('/users/' + existing.id, payload) : await api.post('/users', payload); toast('Benutzer gespeichert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delUser(u) { deleteWithUndo('Benutzer löschen?', `„${u.username}“ wird gelöscht.`, () => api.del('/users/' + u.id), () => render()); }

    // ================= AUDIT (admin) =================
    const AUDIT_ACTIONS = { create: 'Erstellt', update: 'Geändert', delete: 'Gelöscht', login: 'Login', logout: 'Logout' };
    const AUDIT_ENTITIES = { person: 'Person', vehicle: 'Gefährt', category: 'Tarif', service: 'Dienst', charge: 'Zusatzkosten', user: 'Benutzer', photo: 'Foto', passkey: 'Passkey' };

    function fmtAuditVal(x) {
        if (x === null || x === undefined || x === '') return '∅';
        if (typeof x === 'boolean') return x ? 'ja' : 'nein';
        if (typeof x === 'object') return JSON.stringify(x);
        return String(x);
    }
    function auditChangesEl(changes) {
        const box = el('div', { class: 'audit-changes' });
        for (const [field, v] of Object.entries(changes)) {
            box.append(el('div', { class: 'audit-change' },
                el('span', { class: 'audit-field' }, field),
                el('span', { class: 'audit-old' }, fmtAuditVal(v && v.old)),
                el('span', { class: 'audit-arrow' }, '→'),
                el('span', { class: 'audit-new' }, fmtAuditVal(v && v.new))));
        }
        return box;
    }
    function auditItem(a) {
        const head = `${AUDIT_ACTIONS[a.action] || a.action} · ${AUDIT_ENTITIES[a.entity] || a.entity}` + (a.summary ? ' — ' + a.summary : '');
        const li = el('li', {},
            el('div', {}, el('strong', {}, esc(a.username || 'System')), ' · ' + esc(head)),
            el('div', { class: 't-time' }, fmtDateTime(a.created_at)));
        if (a.changes && Object.keys(a.changes).length) li.append(auditChangesEl(a.changes));
        return li;
    }
    routes.audit = async (page) => {
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('⚙', 'Nur für Administratoren.')); return; }
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' }, el('button', { class: 'back-btn', onclick: () => navigate('dashboard') }, '‹'), el('h2', { style: 'margin:0' }, 'Audit-Log')));

        const q = { text: '', action: '', entity: '', offset: 0, limit: 50 };
        const search = el('input', { type: 'search', placeholder: 'Suchen (Benutzer, Beschreibung)…', 'aria-label': 'Audit-Log durchsuchen' });
        const optionList = (map, allLabel) => [el('option', { value: '' }, allLabel), ...Object.entries(map).map(([v, l]) => el('option', { value: v }, l))];
        const actSel = el('select', { 'aria-label': 'Aktion filtern' }, ...optionList(AUDIT_ACTIONS, 'Alle Aktionen'));
        const entSel = el('select', { 'aria-label': 'Objekt filtern' }, ...optionList(AUDIT_ENTITIES, 'Alle Objekte'));
        page.append(el('div', { class: 'card audit-filters' }, search, el('div', { class: 'audit-filter-row' }, actSel, entSel)));

        const ul = el('ul', { class: 'timeline' });
        page.append(el('div', { class: 'card' }, ul));
        const moreBtn = el('button', { class: 'btn btn-ghost btn-block', onclick: () => load(false) }, 'Mehr laden');
        moreBtn.hidden = true;
        page.append(moreBtn);

        async function load(reset) {
            if (reset) { q.offset = 0; ul.innerHTML = ''; }
            const p = new URLSearchParams({ limit: String(q.limit), offset: String(q.offset) });
            if (q.text) p.set('q', q.text);
            if (q.action) p.set('action', q.action);
            if (q.entity) p.set('entity', q.entity);
            let entries = [];
            try { entries = await api.get('/audit?' + p.toString()); } catch { /* ignore */ }
            for (const a of entries) ul.append(auditItem(a));
            q.offset += entries.length;
            moreBtn.hidden = entries.length < q.limit;
            if (!ul.children.length) ul.append(el('li', { class: 'muted' }, 'Keine Einträge.'));
        }
        let t;
        search.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => { q.text = search.value.trim(); load(true); }, 300); });
        actSel.addEventListener('change', () => { q.action = actSel.value; load(true); });
        entSel.addEventListener('change', () => { q.entity = entSel.value; load(true); });
        load(true);
    };

    // ================= SETTINGS (profile / 2FA / sessions) =================
    routes.settings = async (page) => {
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' }, el('button', { class: 'back-btn', onclick: () => navigate('dashboard') }, '‹'), el('h2', { style: 'margin:0' }, 'Einstellungen')));

        // account
        page.append(el('div', { class: 'card' },
            el('div', { class: 'balance' }, el('span', {}, 'Angemeldet als'), el('strong', {}, esc(state.user.username))),
            el('div', { class: 'balance' }, el('span', {}, 'Rolle'), el('span', {}, ROLE_LABEL[state.user.role] || state.user.role)),
            el('button', { class: 'btn btn-ghost btn-block', style: 'margin-top:.7rem', onclick: changePasswordForm }, 'Passwort ändern')));

        // 2FA
        const twoFA = el('div', { class: 'card' }, el('h3', {}, 'Zwei-Faktor-Authentifizierung'));
        if (state.user.totp_enabled) {
            let remaining = null;
            try { remaining = (await api.get('/auth/2fa/backup-codes')).remaining; } catch { /* ignore */ }
            twoFA.append(el('p', { class: 'muted' }, '🔐 2FA ist aktiv.' + (remaining != null ? ` · ${remaining} Recovery-Codes übrig` : '')));
            if (remaining != null && remaining <= 2) twoFA.append(el('p', { class: 'form-error' }, 'Wenige Recovery-Codes übrig – bitte neu generieren.'));
            twoFA.append(el('button', { class: 'btn btn-ghost btn-block', style: 'margin-bottom:.5rem', onclick: regenerateRecoveryCodes }, 'Recovery-Codes neu generieren'));
            twoFA.append(el('button', { class: 'btn btn-danger btn-block', onclick: disable2FA }, '2FA deaktivieren'));
        } else {
            twoFA.append(el('p', { class: 'muted' }, 'Schütze dein Konto mit einer Authenticator-App.'),
                el('button', { class: 'btn btn-primary btn-block', onclick: setup2FA }, '2FA einrichten'));
        }
        page.append(twoFA);

        // passkeys
        if (state.capabilities.passkeys && webauthnSupported()) {
            page.append(passkeysCard());
        }

        // sessions
        const sessCard = el('div', { class: 'card' });
        sessCard.append(el('div', { class: 'page-head' }, el('h3', {}, 'Aktive Sitzungen'),
            el('button', { class: 'btn btn-ghost btn-sm', onclick: revokeOthers }, 'Andere abmelden')));
        const sessions = await api.get('/auth/sessions');
        for (const s of sessions) {
            sessCard.append(el('div', { class: 'balance' },
                el('div', {}, el('div', {}, esc(shortUA(s.user_agent)), s.current ? el('span', { class: 'badge badge-stored', style: 'margin-left:.4rem' }, 'aktuell') : null),
                    el('div', { class: 't-time' }, (s.ip || '?') + ' · zuletzt ' + fmtDateTime(s.last_seen))),
                s.current ? null : el('button', { class: 'btn btn-ghost btn-sm', onclick: () => revokeSession(s.token) }, 'Abmelden')));
        }
        page.append(sessCard);
    };
    function shortUA(ua) {
        if (!ua) return 'Unbekanntes Gerät';
        if (/iphone|ipad|ios/i.test(ua)) return 'iOS-Gerät';
        if (/android/i.test(ua)) return 'Android-Gerät';
        if (/edg/i.test(ua)) return 'Edge';
        if (/chrome/i.test(ua)) return 'Chrome';
        if (/firefox/i.test(ua)) return 'Firefox';
        if (/safari/i.test(ua)) return 'Safari';
        return ua.slice(0, 40);
    }
    async function changePasswordForm() {
        const data = await formModal({
            title: 'Passwort ändern', submitLabel: 'Ändern',
            fields: [
                { name: 'current_password', label: 'Aktuelles Passwort', type: 'password', required: true },
                { name: 'new_password', label: 'Neues Passwort', type: 'password', required: true, minLength: 8, help: 'Mindestens 8 Zeichen.' },
            ],
        });
        if (!data) return;
        try { await api.post('/auth/change-password', { current_password: data.current_password, new_password: data.new_password }); toast('Passwort geändert', 'success'); }
        catch (e) { toast(e.message, 'error'); }
    }
    async function setup2FA() {
        let info;
        try { info = await api.post('/auth/2fa/setup'); } catch (e) { toast(e.message, 'error'); return; }
        contentModal('2FA einrichten', (body, close) => {
            body.append(el('p', { class: 'muted' }, 'Scanne den Code mit deiner Authenticator-App und gib danach den 6-stelligen Code ein.'));
            body.append(el('div', { class: 'qr-box' }, el('img', { src: info.qr, alt: 'QR-Code' })));
            body.append(el('div', { class: 'secret' }, info.secret));
            body.append(el('label', { for: 'totp-enable' }, 'Bestätigungscode'));
            const inp = el('input', { id: 'totp-enable', type: 'text', inputmode: 'numeric', maxlength: 6, placeholder: '123456' });
            body.append(inp);
            const btn = el('button', { class: 'btn btn-primary btn-block', style: 'margin-top:.8rem' }, 'Aktivieren');
            btn.addEventListener('click', async () => {
                try {
                    const res = await api.post('/auth/2fa/enable', { code: inp.value });
                    toast('2FA aktiviert', 'success');
                    close();
                    state.user.totp_enabled = true;
                    showBackupCodes(res.backup_codes || []);
                } catch (e) { toast(e.message, 'error'); }
            });
            body.append(btn);
        });
    }
    // Show one-time backup/recovery codes once, with copy & download.
    function showBackupCodes(codes) {
        contentModal('Backup-Codes', (body, close) => {
            body.append(el('p', { class: 'muted' }, 'Bewahre diese Einmal-Codes sicher auf. Jeder Code funktioniert einmal, falls du keinen Authenticator zur Hand hast. Sie werden nur jetzt angezeigt.'));
            const grid = el('div', { class: 'backup-codes' });
            for (const c of codes) grid.append(el('code', {}, c));
            body.append(grid);
            const text = codes.join('\n');
            const row = el('div', { style: 'display:flex;gap:.5rem;margin-top:.9rem' });
            row.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => { navigator.clipboard && navigator.clipboard.writeText(text); toast('Kopiert', 'success'); } }, 'Kopieren'));
            row.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => downloadText('parkrr-backup-codes.txt', text) }, 'Herunterladen'));
            body.append(row);
            const done = el('button', { class: 'btn btn-primary btn-block', style: 'margin-top:.9rem', onclick: () => { close(); render(); } }, 'Erledigt');
            body.append(done);
        });
    }
    function downloadText(filename, text) {
        const a = el('a', { href: 'data:text/plain;charset=utf-8,' + encodeURIComponent(text), download: filename });
        document.body.append(a); a.click(); a.remove();
    }
    async function disable2FA() {
        const data = await formModal({ title: '2FA deaktivieren', submitLabel: 'Deaktivieren', fields: [{ name: 'password', label: 'Passwort zur Bestätigung', type: 'password', required: true }] });
        if (!data) return;
        try { await api.post('/auth/2fa/disable', { password: data.password }); toast('2FA deaktiviert', 'success'); state.user.totp_enabled = false; render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    async function regenerateRecoveryCodes() {
        const data = await formModal({
            title: 'Recovery-Codes neu generieren', submitLabel: 'Generieren',
            fields: [{ name: 'password', label: 'Passwort zur Bestätigung', type: 'password', required: true, help: 'Alle bisherigen Recovery-Codes werden ungültig.' }],
        });
        if (!data) return;
        try {
            const res = await api.post('/auth/2fa/backup-codes/regenerate', { password: data.password });
            toast('Neue Recovery-Codes erstellt', 'success');
            showBackupCodes(res.backup_codes || []);
        } catch (e) { toast(e.message, 'error'); }
    }
    async function revokeSession(handle) {
        try { await api.del('/auth/sessions/' + handle); toast('Sitzung abgemeldet', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    async function revokeOthers() {
        if (!await confirmDialog('Andere Sitzungen abmelden?', 'Alle anderen Geräte werden abgemeldet.', 'Abmelden')) return;
        try { await api.post('/auth/sessions/revoke-others'); toast('Andere Sitzungen abgemeldet', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }

    // ================= MENU SHEET =================
    function openMenu() {
        const dlg = $('#menu');
        const body = $('#menu-body');
        body.innerHTML = '';
        body.append(el('div', { class: 'sheet-user' },
            el('div', { class: 'name' }, esc(state.user.username)),
            el('div', { class: 'muted' }, ROLE_LABEL[state.user.role] || state.user.role)));
        const item = (icon, label, fn, cls = '') => {
            const b = el('button', { class: 'menu-item ' + cls }, el('span', { class: 'ic' }, icon), el('span', {}, label));
            b.addEventListener('click', () => { dlg.close(); fn(); });
            return b;
        };
        body.append(item('⚙', 'Einstellungen', () => navigate('settings')));
        if (isAdmin()) body.append(item('👤', 'Benutzer', () => navigate('users')));
        if (isAdmin()) body.append(item('🗒', 'Audit-Log', () => navigate('audit')));
        body.append(item('◐', 'Design wechseln', () => toggleTheme()));
        body.append(item('⎋', 'Abmelden', () => logout(), 'danger'));
        dlg.showModal();
    }

    // ---------- auth / bootstrap ----------
    async function showApp() {
        $('#login-view').hidden = true;
        $('#app-view').hidden = false;
        if (!location.hash || location.hash === '#') location.hash = '#/dashboard';
        else render();
    }
    function showLogin() {
        $('#app-view').hidden = true;
        $('#login-view').hidden = false;
        $('#login-totp-wrap').hidden = true;
        const pkBtn = $('#passkey-login');
        if (pkBtn) pkBtn.hidden = !(state.capabilities.passkeys && webauthnSupported());
        $('#login-username').focus();
    }
    async function logout() {
        try { await api.post('/auth/logout'); } catch { /* ignore */ }
        state.user = null;
        showLogin();
    }

    // ---------- passkeys (WebAuthn) ----------
    const webauthnSupported = () => typeof window.PublicKeyCredential !== 'undefined';
    const b64uToBuf = (s) => {
        s = String(s).replace(/-/g, '+').replace(/_/g, '/');
        const pad = s.length % 4 ? '='.repeat(4 - (s.length % 4)) : '';
        const bin = atob(s + pad);
        const buf = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
        return buf.buffer;
    };
    const bufToB64u = (buf) => {
        const bytes = new Uint8Array(buf);
        let bin = '';
        for (const b of bytes) bin += String.fromCharCode(b);
        return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    };

    async function passkeyRegister() {
        if (!webauthnSupported()) { toast('Dieses Gerät unterstützt keine Passkeys', 'error'); return; }
        try {
            const opts = await api.post('/passkeys/register/begin', {});
            const pk = opts.publicKey;
            pk.challenge = b64uToBuf(pk.challenge);
            pk.user.id = b64uToBuf(pk.user.id);
            if (pk.excludeCredentials) pk.excludeCredentials.forEach((c) => { c.id = b64uToBuf(c.id); });
            const cred = await navigator.credentials.create({ publicKey: pk });
            await api.post('/passkeys/register/finish', {
                id: cred.id, rawId: bufToB64u(cred.rawId), type: cred.type,
                response: {
                    attestationObject: bufToB64u(cred.response.attestationObject),
                    clientDataJSON: bufToB64u(cred.response.clientDataJSON),
                    transports: cred.response.getTransports ? cred.response.getTransports() : [],
                },
                clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {},
            });
            toast('Passkey hinzugefügt', 'success');
            render();
        } catch (e) {
            if (e && e.name === 'NotAllowedError') return; // user cancelled the prompt
            toast(e.message || 'Passkey-Registrierung fehlgeschlagen', 'error');
        }
    }

    async function passkeyLogin() {
        if (!webauthnSupported()) { toast('Dieses Gerät unterstützt keine Passkeys', 'error'); return; }
        const errEl = $('#login-error'); errEl.hidden = true;
        try {
            const opts = await api.post('/auth/passkey/login/begin', {});
            const pk = opts.publicKey;
            pk.challenge = b64uToBuf(pk.challenge);
            if (pk.allowCredentials) pk.allowCredentials.forEach((c) => { c.id = b64uToBuf(c.id); });
            const cred = await navigator.credentials.get({ publicKey: pk });
            state.user = await api.post('/auth/passkey/login/finish', {
                id: cred.id, rawId: bufToB64u(cred.rawId), type: cred.type,
                response: {
                    authenticatorData: bufToB64u(cred.response.authenticatorData),
                    clientDataJSON: bufToB64u(cred.response.clientDataJSON),
                    signature: bufToB64u(cred.response.signature),
                    userHandle: cred.response.userHandle ? bufToB64u(cred.response.userHandle) : null,
                },
                clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {},
            });
            showApp();
        } catch (e) {
            if (e && e.name === 'NotAllowedError') return; // user cancelled
            errEl.textContent = e.message || 'Passkey-Anmeldung fehlgeschlagen';
            errEl.hidden = false;
        }
    }

    function passkeysCard() {
        const card = el('div', { class: 'card' }, el('h3', {}, 'Passkeys'),
            el('p', { class: 'muted' }, 'Anmeldung per Fingerabdruck/Gesichtserkennung – ohne Passwort.'));
        const list = el('div', {});
        card.append(list);
        const refresh = async () => {
            list.innerHTML = '';
            let creds = [];
            try { creds = await api.get('/passkeys'); } catch { /* ignore */ }
            if (!creds.length) { list.append(el('p', { class: 'muted' }, 'Noch keine Passkeys.')); return; }
            for (const c of creds) {
                list.append(el('div', { class: 'balance' },
                    el('div', {}, el('div', {}, esc(c.name)),
                        el('div', { class: 't-time' }, 'seit ' + fmtDate(c.created_at) +
                            (c.last_used_at ? ' · zuletzt ' + fmtDate(c.last_used_at) : ''))),
                    el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delPasskey(c) }, 'Entfernen')));
            }
        };
        card.append(el('button', { class: 'btn btn-primary btn-block', style: 'margin-top:.6rem', onclick: passkeyRegister }, '+ Passkey hinzufügen'));
        refresh();
        return card;
    }
    async function delPasskey(c) {
        if (!await confirmDialog('Passkey entfernen?', `„${c.name}“ wird entfernt.`, 'Entfernen')) return;
        try { await api.del('/passkeys/' + c.id); toast('Passkey entfernt', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }

    function bindStatic() {
        $('#login-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const errEl = $('#login-error'); errEl.hidden = true;
            const body = { username: $('#login-username').value, password: $('#login-password').value };
            const totp = $('#login-totp').value.trim();
            if (totp) body.totp_code = totp;
            try {
                state.user = await api.post('/auth/login', body);
                $('#login-password').value = ''; $('#login-totp').value = '';
                showApp();
            } catch (err) {
                if (err.data && err.data.totp_required) {
                    $('#login-totp-wrap').hidden = false;
                    $('#login-totp').focus();
                    errEl.textContent = totp ? err.message : 'Bitte Zwei-Faktor-Code eingeben.';
                } else {
                    errEl.textContent = err.message;
                }
                errEl.hidden = false;
            }
        });
        const pkBtn = $('#passkey-login');
        if (pkBtn) pkBtn.addEventListener('click', passkeyLogin);
        $('#theme-btn').addEventListener('click', toggleTheme);
        $('#menu-btn').addEventListener('click', openMenu);
        $$('.tab').forEach((t) => t.addEventListener('click', () => navigate(t.dataset.route)));
        for (const id of ['#modal', '#confirm', '#menu']) {
            $(id).addEventListener('click', (e) => { if (e.target === e.currentTarget) e.currentTarget.close(); });
        }
        window.addEventListener('hashchange', () => { if (state.user) render(); });
    }

    // PWA install prompt: offer installation when the browser allows it.
    let deferredInstall = null;
    function setupInstallPrompt() {
        window.addEventListener('beforeinstallprompt', (e) => {
            e.preventDefault();
            deferredInstall = e;
            toastAction('Parkrr als App installieren?', 'Installieren', async () => {
                if (!deferredInstall) return;
                deferredInstall.prompt();
                await deferredInstall.userChoice;
                deferredInstall = null;
            }, 8000);
        });
    }
    // Offline indicator: a banner plus a body class while the network is down.
    function setupOfflineIndicator() {
        const banner = el('div', { class: 'offline-banner', role: 'status', 'aria-live': 'polite', hidden: true }, t('offline.banner'));
        document.body.append(banner);
        const update = () => {
            const off = navigator.onLine === false;
            banner.hidden = !off;
            document.body.classList.toggle('is-offline', off);
        };
        window.addEventListener('online', update);
        window.addEventListener('offline', update);
        update();
    }

    async function init() {
        initTheme();
        bindStatic();
        setupInstallPrompt();
        setupOfflineIndicator();
        try { state.capabilities = await api.get('/auth/capabilities'); } catch { state.capabilities = {}; }
        try { state.user = await api.get('/auth/me'); showApp(); }
        catch { showLogin(); }
        if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
    }
    document.addEventListener('DOMContentLoaded', init);
})();
