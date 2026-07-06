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

    // ---------- API ----------
    const api = {
        async request(method, path, body) {
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
    const state = { user: null, persons: [], categories: [], services: [] };

    // permission helpers
    const isAdmin = () => !!(state.user && state.user.is_admin);
    const role = () => (state.user ? state.user.role : '');
    const canManage = () => isAdmin() || role() === 'manager';
    const canBill = () => isAdmin() || role() === 'manager' || role() === 'accounting';

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
                    // Open the native date/time picker on tap anywhere in the field.
                    if (f.type === 'date' || f.type === 'time' || f.type === 'datetime-local') {
                        const openPicker = () => { try { input.showPicker(); } catch { /* not supported */ } };
                        input.addEventListener('focus', openPicker);
                        input.addEventListener('click', openPicker);
                    }
                }
                body.append(input);
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
                for (const f of fields) {
                    const node = $('#f_' + f.name, body);
                    data[f.name] = f.type === 'checkbox' ? node.checked : node.value;
                }
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
    const ROLE_LABEL = { admin: 'Administrator', manager: 'Standortleiter', accounting: 'Buchhaltung', readonly: 'Nur-Lesen' };
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
        $$('.tab').forEach((t) => t.classList.toggle('active', t.dataset.route === (TAB_FOR[routeName] || TAB_FOR[name])));
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
            el('div', { class: 'balance' }, el('span', {}, stats.has_flat_rate ? 'Pauschale' : 'Aufgelaufene Miete'), el('span', { class: 'amt' }, eur(stats.total_accrued))),
            el('div', { class: 'balance' }, el('span', {}, 'Zusatzkosten'), el('span', { class: 'amt' }, eur(stats.total_charges))),
            el('div', { class: 'balance' }, el('span', {}, 'Bezahlt (per Slider)'), el('span', { class: 'amt' }, '− ' + eur(stats.total_paid))),
            el('div', { class: 'balance' }, el('strong', {}, 'Offener Saldo'), el('strong', { class: 'amt ' + balCls }, eur(stats.balance)))));

        // flat rate (Pauschale) card
        if (canBill() || stats.has_flat_rate) {
            const frCard = el('div', { class: 'card' });
            frCard.append(el('div', { class: 'card-row' },
                el('h3', { style: 'margin:0' }, 'Pauschale'),
                canBill() ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => flatRateForm(id, stats) },
                    stats.has_flat_rate ? '✎ Ändern' : '+ Einrichten') : null));
            if (stats.has_flat_rate) {
                const unit = stats.flat_rate_period === 'yearly' ? '/Jahr' : '/Monat';
                frCard.append(el('div', { class: 'card-meta', style: 'margin:.4rem 0' },
                    `${eur(stats.flat_rate)}${unit} · deckt alle Gefährte · seit ${fmtDate(stats.flat_rate_start)}` +
                    (stats.flat_rate_end ? ` bis ${fmtDate(stats.flat_rate_end)}` : '') +
                    ` · aufgelaufen ${eur(stats.flat_rate_accrued)}`));
                frCard.append(el('div', { class: 'card-meta', style: 'margin-top:.2rem' }, 'Bezahlstatus je Jahr unter „Kosten pro Jahr".'));
            } else {
                frCard.append(el('div', { class: 'card-meta', style: 'margin-top:.3rem' }, 'Keine Pauschale – Abrechnung je Gefährt.'));
            }
            page.append(frCard);
        }

        // monthly chart
        const chartCard = el('div', { class: 'chart-card' }, el('h3', {}, (stats.has_flat_rate ? 'Pauschale pro Monat · ' : 'Aufgelaufene Miete pro Monat · ') + stats.year));
        chartCard.append(chartBars(stats.monthly_accrued, MONTHS));
        page.append(chartCard);

        // years — for a flat rate each year has its own open/paid slider
        if (stats.years.length) {
            const yc = el('div', { class: 'chart-card' }, el('h3', {}, 'Kosten pro Jahr'));
            if (stats.has_flat_rate) {
                for (const y of stats.years) {
                    const row = el('div', { class: 'balance', style: 'align-items:center' },
                        el('span', {}, String(y.year) + ' · ' + eur(y.cost)),
                        canBill() ? flatYearPaidSlider(id, y.year, y.paid)
                            : el('span', { class: 'badge ' + (y.paid ? 'badge-active' : 'badge-ended') }, y.paid ? 'bezahlt' : 'offen'));
                    yc.append(row);
                }
            } else {
                const max = Math.max(...stats.years.map((y) => y.cost), 1);
                const bars = el('div', { class: 'bars' });
                for (const y of stats.years) bars.append(el('div', { class: 'bar-row' },
                    el('div', {}, String(y.year)),
                    el('div', { class: 'bar-track' }, el('div', { class: 'bar-fill', style: `width:${(y.cost / max) * 100}%` })),
                    el('div', { class: 'bar-val' }, eur(y.cost))));
                yc.append(bars);
            }
            page.append(yc);
        }

        // vehicles
        const vh = el('div', { class: 'page-head' }, el('h3', {}, 'Gefährte (' + vehicles.length + ')'));
        if (canManage()) vh.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => vehicleForm(null, id) }, '+ Gefährt'));
        page.append(vh);
        if (!vehicles.length) page.append(el('p', { class: 'muted' }, 'Keine Gefährte.'));
        else vehicles.forEach((v) => page.append(vehicleCard(v, { linkable: true, hidePaid: stats.has_flat_rate })));

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

    function vehicleCard(v, { linkable = true, hidePaid = false } = {}) {
        const title = v.label || v.license_plate || v.category_name;
        const rateUnit = v.billing_period === 'yearly' ? '/Jahr' : '/Monat';
        const main = el('div', { style: 'flex:1;' + (linkable ? 'cursor:pointer' : ''), onclick: linkable ? () => navigate('vehicles/' + v.id) : null },
            el('h3', {}, esc(title)),
            el('div', { class: 'card-meta' }, el('span', { class: 'badge badge-cat' }, esc(v.category_name)), ' ', esc(v.person_name),
                v.photo_count ? el('span', { class: 'muted' }, '  📷 ' + v.photo_count) : null),
            el('div', { class: 'card-meta' }, `${eur(v.effective_rate)}${rateUnit}` + (v.cost_override != null ? ' (Sonderpreis)' : '') +
                ` · seit ${fmtDate(v.start_date)}` + (v.end_date ? ` bis ${fmtDate(v.end_date)}` : '')),
            // For a flat-rate person the per-vehicle accrued cost is not billed
            // (the flat rate covers it), so showing it here would contradict the
            // balance. Suppress it in that case.
            hidePaid ? null
                : el('div', { class: 'card-meta', style: 'color:var(--text);font-weight:600;margin-top:.3rem' }, 'Aufgelaufen: ' + eur(v.accrued_cost)));
        const actions = el('div', { class: 'card-actions' },
            canManage() && el('button', { class: 'btn btn-ghost btn-sm', onclick: () => vehicleForm(v) }, '✎'),
            canManage() && el('button', { class: 'btn btn-ghost btn-sm', onclick: () => delVehicle(v) }, '🗑'));
        return el('div', { class: 'card' },
            el('div', { class: 'card-row' }, main, actions),
            vehicleControls(v, hidePaid));
    }

    // Slider-based quick controls: status + payment, shown on card and detail.
    // When hidePaid is set (person on a flat rate) the payment slider is replaced
    // by a hint that the vehicle is covered by the flat rate.
    function vehicleControls(v, hidePaid = false) {
        const wrap = el('div', { class: 'controls-row' });
        wrap.append(statusSlider(v));
        if (hidePaid) wrap.append(el('span', { class: 'badge badge-cat', title: 'Kosten über die Pauschale abgerechnet' }, 'in Pauschale'));
        else if (canBill()) wrap.append(paidSlider(v));
        if (canManage() && v.status === 'collected') {
            wrap.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => duplicateVehicle(v) }, '↻ Erneut einstellen'));
        }
        return wrap;
    }

    // Per-year paid slider for the flat rate (Pauschale).
    function flatYearPaidSlider(personId, year, paid) {
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus ' + year });
        const setPaid = async (val, e) => {
            markActive(e.currentTarget);
            try { await api.post('/persons/' + personId + '/flatrate/paid', { year, paid: val }); toast(val ? year + ' bezahlt' : year + ' offen', 'success'); render(); }
            catch (err) { toast(err.message, 'error'); render(); }
        };
        seg.append(el('button', { class: (!paid ? 'active open' : ''), type: 'button', role: 'radio', 'aria-checked': String(!paid), 'aria-label': 'offen', onclick: (e) => setPaid(false, e) }, 'offen'));
        seg.append(el('button', { class: (paid ? 'active done' : ''), type: 'button', role: 'radio', 'aria-checked': String(paid), 'aria-label': 'bezahlt', onclick: (e) => setPaid(true, e) }, 'bezahlt'));
        return seg;
    }

    async function flatRateForm(personId, stats) {
        const data = await formModal({
            title: 'Pauschale',
            submitLabel: 'Speichern',
            fields: [
                { name: 'enabled', label: 'Pauschale aktiv (statt Abrechnung je Gefährt)', type: 'checkbox', value: !!stats.has_flat_rate },
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', min: 0, value: stats.flat_rate ?? '' },
                { name: 'period', label: 'Zeitraum', type: 'select', value: stats.flat_rate_period || 'monthly', options: [{ value: 'monthly', label: 'pro Monat' }, { value: 'yearly', label: 'pro Jahr' }] },
                { name: 'start_date', label: 'Gültig ab', type: 'date', value: stats.flat_rate_start ? stats.flat_rate_start.slice(0, 10) : today() },
                { name: 'end_date', label: 'Gültig bis (optional)', type: 'date', value: stats.flat_rate_end ? stats.flat_rate_end.slice(0, 10) : '', help: 'Leer lassen für laufend.' },
            ],
        });
        if (!data) return;
        const payload = {
            enabled: data.enabled,
            amount: data.amount === '' ? null : Number(data.amount),
            period: data.period,
            start_date: data.start_date,
            end_date: data.end_date === '' ? null : data.end_date,
        };
        try { await api.put('/persons/' + personId + '/flatrate', payload); toast('Pauschale gespeichert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
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
            // Status / end / reservation are managed via the sliders; preserve
            // existing values here so editing master data never wipes them.
            status: existing?.status || 'stored',
            end_date: existing?.end_date ? existing.end_date.slice(0, 10) : null,
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
        let note = '';
        if (!opts.silent && (s === 'collected' || s === 'cancelled')) {
            const d = await formModal({ title: STATUS_LABEL[s], submitLabel: 'Bestätigen', fields: [{ name: 'note', label: 'Notiz (optional)', type: 'textarea' }] });
            if (!d) return; note = d.note;
        }
        try {
            await api.post('/vehicles/' + v.id + '/status', { status: s, note });
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
                { name: 'password', label: existing ? 'Neues Passwort (optional)' : 'Passwort', type: 'password', help: 'Mindestens 8 Zeichen.' },
                { name: 'role', label: 'Rolle', type: 'select', value: existing?.role || 'manager', options: Object.entries(ROLE_LABEL).map(([v, l]) => ({ value: v, label: l })) },
            ],
        });
        if (!data) return;
        const payload = { username: data.username, email: data.email, role: data.role, password: data.password };
        try { existing ? await api.put('/users/' + existing.id, payload) : await api.post('/users', payload); toast('Benutzer gespeichert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delUser(u) { deleteWithUndo('Benutzer löschen?', `„${u.username}“ wird gelöscht.`, () => api.del('/users/' + u.id), () => render()); }

    // ================= AUDIT (admin) =================
    routes.audit = async (page) => {
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('⚙', 'Nur für Administratoren.')); return; }
        const entries = await api.get('/audit?limit=200');
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' }, el('button', { class: 'back-btn', onclick: () => navigate('dashboard') }, '‹'), el('h2', { style: 'margin:0' }, 'Audit-Log')));
        if (!entries.length) { page.append(emptyState('🗒', 'Keine Einträge.')); return; }
        const ul = el('ul', { class: 'timeline' });
        for (const a of entries) {
            ul.append(el('li', {},
                el('div', {}, el('strong', {}, esc(a.username || 'System')), ' ' + esc(a.action) + ' · ' + esc(a.entity) + (a.summary ? ' — ' + esc(a.summary) : '')),
                el('div', { class: 't-time' }, fmtDateTime(a.created_at))));
        }
        page.append(el('div', { class: 'card' }, ul));
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
                { name: 'new_password', label: 'Neues Passwort', type: 'password', required: true, help: 'Mindestens 8 Zeichen.' },
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
        $('#login-username').focus();
    }
    async function logout() {
        try { await api.post('/auth/logout'); } catch { /* ignore */ }
        state.user = null;
        showLogin();
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
    // Simple offline indicator (a banner while the network is down).
    function setupOfflineIndicator() {
        const banner = el('div', { class: 'offline-banner', role: 'status', hidden: true }, 'Offline – Änderungen sind derzeit nicht möglich.');
        document.body.append(banner);
        const update = () => { banner.hidden = navigator.onLine; };
        window.addEventListener('online', update);
        window.addEventListener('offline', update);
        update();
    }

    async function init() {
        initTheme();
        bindStatic();
        setupInstallPrompt();
        setupOfflineIndicator();
        try { state.user = await api.get('/auth/me'); showApp(); }
        catch { showLogin(); }
        if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
    }
    document.addEventListener('DOMContentLoaded', init);
})();
