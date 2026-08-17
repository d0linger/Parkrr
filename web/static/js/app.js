/* Parkrr SPA – vanilla JS, no external dependencies. */
(() => {
    'use strict';

    // ---------- client-error telemetry (AR4): report uncaught SPA errors to the
    // server (auth-gated, rate-limited, no PII). Capped per session so a repeating
    // error can't spam. Never throws itself. ----------
    const APP_VERSION = ((document.querySelector('meta[name="app-version"]') || {}).content) || 'dev'; // server-injected build version
    (function () {
        let sent = 0;
        const report = (message, stack) => {
            if (sent >= 5 || !message) return; sent++;
            try {
                // /api/client-error is auth-gated → carry the CSRF token like every other write.
                fetch('/api/client-error', {
                    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookie('parkrr_csrf') }, keepalive: true,
                    body: JSON.stringify({ message: String(message).slice(0, 500), stack: String(stack || '').slice(0, 4000), url: location.pathname, version: APP_VERSION }),
                }).catch(() => { });
            } catch (e) { /* never let reporting break the app */ }
        };
        window.addEventListener('error', (e) => report(e.message, e.error && e.error.stack));
        window.addEventListener('unhandledrejection', (e) => { const r = e.reason; report((r && r.message) || 'unhandledrejection', r && r.stack); });
    })();

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
    // Coerces to a string for use as element text (via el()/textContent, which is
    // XSS-safe). NOT an HTML escaper — never concatenate esc() into innerHTML; build
    // markup from DOM nodes instead (see setPending, chart tooltips use static data).
    const esc = (s) => String(s ?? '');
    const getCookie = (name) => {
        const m = document.cookie.match('(^|;)\\s*' + name + '\\s*=\\s*([^;]+)');
        return m ? decodeURIComponent(m.pop()) : '';
    };
    const eur = (n) => new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(Number(n) || 0);
    const fmtDate = (s) => (s ? new Date(s).toLocaleDateString('de-DE') : '–');
    const fmtDateTime = (s) => (s ? new Date(s).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' }) : '–');
    // Local calendar date (not UTC): toISOString() would yield yesterday between
    // local midnight and the UTC offset, wrong-dating a payment/charge default.
    const today = () => {
        const d = new Date();
        return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
    };
    const norm = (s) => String(s ?? '').toLowerCase();

    // ---------- icons ----------
    // Consistent stroke icons (inline SVG, themable via currentColor) instead
    // of platform emojis, which render differently on every OS.
    const ICONS = {
        edit: '<path d="M4 20l1-4L16.5 4.5a2.1 2.1 0 0 1 3 3L8 19l-4 1Z"/><path d="M14.5 6.5l3 3"/>',
        trash: '<path d="M4.5 7h15M9.5 7V5.2A1.2 1.2 0 0 1 10.7 4h2.6a1.2 1.2 0 0 1 1.2 1.2V7M6.5 7l1 12a1.5 1.5 0 0 0 1.5 1.4h6a1.5 1.5 0 0 0 1.5-1.4l1-12"/><path d="M10 11v6M14 11v6"/>',
        camera: '<path d="M4 8.5A1.5 1.5 0 0 1 5.5 7H8l1.4-2h5.2L16 7h2.5A1.5 1.5 0 0 1 20 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 17.5v-9Z"/><circle cx="12" cy="13" r="3.4"/>',
        redo: '<path d="M4 12a8 8 0 1 1 2.4 5.7"/><path d="M4 18v-5h5"/>',
        undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 6 6v4"/>',
        close: '<path d="M6 6l12 12M18 6 6 18"/>',
        settings: '<path d="M6 4v7M6 15v5M12 4v3M12 11v9M18 4v11M18 19v1"/><circle cx="6" cy="13" r="1.8"/><circle cx="12" cy="9" r="1.8"/><circle cx="18" cy="17" r="1.8"/>',
        users: '<circle cx="9" cy="8" r="3.4"/><path d="M3.5 20c.6-3.4 2.9-5.2 5.5-5.2s4.9 1.8 5.5 5.2"/><path d="M16 5.4a3.4 3.4 0 0 1 0 5.9M17.8 14.9c1.6.8 2.6 2.4 3 5.1"/>',
        log: '<rect x="5" y="3.5" width="14" height="17" rx="2"/><path d="M9 8.5h6M9 12h6M9 15.5h4"/>',
        upload: '<path d="M12 15V4M8 8l4-4 4 4"/><path d="M5 15v3a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3"/>',
        download: '<path d="M12 4v11M8 11l4 4 4-4"/><path d="M5 19h14"/>',
        mail: '<rect x="3.5" y="5.5" width="17" height="13" rx="2"/><path d="M4 7.5l8 6 8-6"/>',
        theme: '<path d="M12 3a9 9 0 1 0 9 9 7 7 0 0 1-9-9Z"/>',
        logout: '<path d="M9 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h3M15 8l4 4-4 4M19 12H9"/>',
        box: '<path d="M3 8l9-5 9 5v8l-9 5-9-5V8Z"/><path d="M3 8l9 5 9-5M12 13v8"/>',
        car: '<path d="M4 16v-3.2c0-.6.2-1.2.6-1.7l2-2.6c.4-.5 1-.8 1.6-.8h6.4c.7 0 1.3.3 1.7.9l1.9 2.5c.4.5.6 1.1.6 1.7V16"/><circle cx="7.5" cy="17" r="1.8"/><circle cx="16.5" cy="17" r="1.8"/><path d="M3 16h18"/>',
        shield: '<path d="M12 3l7 3v6c0 4-3 6.5-7 8-4-1.5-7-4-7-8V6l7-3Z"/><path d="M9 12l2 2 4-4"/>',
        unlock: '<rect x="4.5" y="10.5" width="15" height="9" rx="2"/><path d="M8 10.5V7a4 4 0 0 1 7.5-1.5"/>',
        alert: '<path d="M12 3.5 22 20H2L12 3.5Z"/><path d="M12 10v4M12 17.3v.2"/>',
        key: '<circle cx="7.6" cy="15.4" r="4"/><path d="M10.4 12.6 20 3M16.5 6.5l2.5 2.5M14 9l2 2"/>',
        power: '<path d="M12 4v8"/><path d="M7.4 6.8a7 7 0 1 0 9.2 0"/>',
        chevron: '<path d="M9 6l6 6-6 6"/>',
        tag: '<path d="M12.6 3.6 20.4 11.4a2 2 0 0 1 0 2.8l-6.2 6.2a2 2 0 0 1-2.8 0L3.6 12.6V5.6a2 2 0 0 1 2-2h7Z"/><circle cx="8.3" cy="8.3" r="1.3"/>',
        receipt: '<path d="M6 3h12v18l-2.2-1.3-2 1.3-2-1.3-2 1.3-2-1.3L6 21V3Z"/><path d="M9.2 8.5h5.6M9.2 12h5.6"/>',
        check: '<path d="M20 6 9 17l-5-5"/>',
        plus: '<path d="M12 5v14M5 12h14"/>',
        archive: '<rect x="3.5" y="5" width="17" height="4" rx="1"/><path d="M5 9v9a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9M10 13h4"/>',
        pin: '<path d="M12 21s-6-5.3-6-10a6 6 0 0 1 12 0c0 4.7-6 10-6 10Z"/><circle cx="12" cy="11" r="2.3"/>',
    };
    const icon = (name, size = 16) => {
        const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        s.setAttribute('viewBox', '0 0 24 24');
        s.setAttribute('width', size);
        s.setAttribute('height', size);
        s.setAttribute('fill', 'none');
        s.setAttribute('stroke', 'currentColor');
        s.setAttribute('stroke-width', '1.8');
        s.setAttribute('stroke-linecap', 'round');
        s.setAttribute('stroke-linejoin', 'round');
        s.setAttribute('aria-hidden', 'true');
        s.classList.add('ic-svg');
        s.innerHTML = ICONS[name] || '';
        return s;
    };

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
        async upload(path, formData, method = 'POST') {
            const res = await fetch('/api' + path, {
                method, body: formData, credentials: 'same-origin',
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
    // QW2 — every toast is routed through the i18n layer t(). We use the gettext
    // convention (the source string IS the key): today German passes straight
    // through (t returns the key when untranslated), and a future language just
    // adds MESSAGES.<lang>['<german phrase>'] = '<translation>' — no call-site edits.
    function toast(msg, kind = '') {
        const box = $('#toast');
        box.innerHTML = '';
        box.append(document.createTextNode(t(msg)));
        box.className = 'toast' + (kind ? ' ' + kind : '');
        box.hidden = false;
        clearTimeout(toastTimer);
        toastTimer = setTimeout(() => { box.hidden = true; }, 3200);
    }
    function toastAction(msg, actionLabel, onAction, ms = 4500) {
        const box = $('#toast');
        clearTimeout(toastTimer);
        box.innerHTML = '';
        box.className = 'toast';
        box.hidden = false;
        box.append(document.createTextNode(t(msg) + '  '));
        const btn = el('button', { class: 'toast-undo', type: 'button' }, t(actionLabel));
        btn.addEventListener('click', () => { box.hidden = true; onAction(); });
        box.append(btn);
        toastTimer = setTimeout(() => { box.hidden = true; }, ms);
    }

    // ---------- confirm ----------
    function confirmDialog(title, message, okLabel = 'Löschen') {
        return new Promise((resolve) => {
            const dlg = $('#confirm');
            $('#confirm-title').textContent = title;
            $('#confirm-body').textContent = message;
            const ok = $('#confirm-ok'); const cancel = $('#confirm-cancel');
            ok.textContent = okLabel;
            let settled = false;
            const done = (v) => {
                ok.removeEventListener('click', onOk);
                cancel.removeEventListener('click', onCancel);
                if (settled) return;
                settled = true;
                resolve(v); // resolve before close so the close-event's done(false) is a no-op
                dlg.close();
            };
            const onOk = () => done(true); const onCancel = () => done(false);
            ok.addEventListener('click', onOk); cancel.addEventListener('click', onCancel);
            // Backdrop click / Escape close the dialog without a button — treat as
            // cancel and detach the listeners so they never accumulate on the shared
            // #confirm buttons (otherwise a re-opened confirm could fire a previously
            // backdrop-cancelled action).
            dlg.addEventListener('close', () => done(false), { once: true });
            dlg.showModal();
        });
    }

    // Step-up re-authentication (SH-02): a sensitive change (enabling 2FA, adding a
    // passkey) is allowed without a password shortly after login; once that window
    // closes the server answers 403 "reauth_required". withStepUp runs the action,
    // and on that signal prompts for the account password and retries once. A
    // cancelled prompt throws an error flagged { cancelled: true } so callers can
    // bail silently.
    async function promptStepUpPassword() {
        const data = await formModal({
            title: 'Bestätigung erforderlich',
            submitLabel: 'Bestätigen',
            fields: [{ name: 'password', label: 'Kontopasswort', type: 'password', required: true, help: 'Zur Sicherheit bei sicherheitsrelevanten Änderungen.' }],
        });
        return data ? data.password : null;
    }
    async function withStepUp(doCall) {
        try {
            return await doCall('');
        } catch (e) {
            if (!e || (e.message || '') !== 'reauth_required') throw e;
            const pw = await promptStepUpPassword();
            if (!pw) throw Object.assign(new Error('Abgebrochen'), { cancelled: true });
            return await doCall(pw);
        }
    }

    // delete with an undo window (delayed server call)
    async function deleteWithUndo(title, message, doDelete, onDone, node) {
        if (!await confirmDialog(title, message)) return;
        let cancelled = false;
        // Optimistic: the entry disappears immediately; the API call only runs
        // once the undo window has passed. Undo (or a failure) brings it back.
        if (node) node.hidden = true;
        const timer = setTimeout(async () => {
            if (cancelled) return;
            try { await doDelete(); toast('Gelöscht', 'success'); onDone && onDone(); }
            catch (e) { toast(e.message, 'error'); if (node) node.hidden = false; onDone && onDone(); }
        }, 4500);
        toastAction('Wird gelöscht …', 'Rückgängig', () => { cancelled = true; clearTimeout(timer); if (node) node.hidden = false; toast('Abgebrochen'); });
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

    // save: optional async (data) => Promise. When given, formModal awaits it on
    // submit — showing a "Speichern…" busy button, keeping the modal open, and
    // surfacing a thrown error inline (with retry) instead of closing first.
    function formModal({ title, fields, submitLabel = 'Speichern', onRender = null, save = null, danger = null }) {
        return new Promise((resolve) => {
            const dlg = $('#modal');
            $('#modal-title').textContent = title;
            $('#modal-submit').textContent = submitLabel;
            $('#modal-submit').style.display = '';
            // Destructive forms turn the confirm button red; reset on every (re)open
            // so a prior danger form never leaves the shared button red.
            $('#modal-submit').classList.toggle('btn-danger', !!danger);
            $('#modal-submit').classList.toggle('btn-primary', !danger);
            $('#modal-submit').disabled = false; // a prior form (e.g. onRender gating) may have disabled it
            $('#modal')._canClose = null; // never inherit a prior gated contentModal's close guard
            $('#modal-cancel').disabled = false; $('#modal-close').disabled = false; // clear a prior busy state
            $('#modal-cancel').style.display = '';
            const body = $('#modal-body');
            body.innerHTML = '';
            // Async-save error banner (used only when `save` is provided).
            const formErr = el('p', { class: 'form-error', id: 'modal-form-error', role: 'alert', hidden: true });
            body.append(formErr);
            if (danger) body.append(el('p', { class: 'danger-note' }, icon('alert', 16), el('span', {}, danger)));
            for (const f of fields) {
                if (f.type === 'checkbox') {
                    const input = el('input', { type: 'checkbox', id: 'f_' + f.name, name: f.name });
                    if (f.value) input.checked = true;
                    body.append(el('label', { class: 'switch', for: 'f_' + f.name }, input, el('span', { class: 'track' }), el('span', {}, f.label)));
                    continue;
                }
                const id = 'f_' + f.name;
                const errId = 'err_' + f.name;
                const helpId = 'help_' + f.name;
                // The required asterisk is decorative — aria-required carries the
                // meaning, so hide the star from screen readers.
                body.append(el('label', { for: id }, f.label,
                    f.required ? el('span', { 'aria-hidden': 'true' }, ' *') : null));
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
                // Link the field to its error message + help text so a screen
                // reader reads both when the field is focused, not just "invalid".
                if (f.required) input.setAttribute('aria-required', 'true');
                input.setAttribute('aria-describedby', f.help ? errId + ' ' + helpId : errId);
                // Password fields get a show/hide affix toggle to catch typos.
                if (f.type === 'password') body.append(el('div', { class: 'input-affix' }, input, pwToggleBtn(input)));
                else body.append(input);
                body.append(el('div', { class: 'field-error', id: errId, role: 'alert', hidden: true }));
                if (f.help) body.append(el('div', { class: 'card-meta', id: helpId }, f.help));
            }
            const form = $('#modal-form');
            let settled = false;
            const settle = (v) => { if (!settled) { settled = true; resolve(v); } };
            const cleanup = () => {
                form.removeEventListener('submit', onSubmit);
                $('#modal-cancel').removeEventListener('click', onCancel);
                $('#modal-close').removeEventListener('click', onCancel);
            };
            // Detach the submit handler on ANY close path (buttons, Escape, backdrop
            // click). Otherwise a re-opened form accumulates stale submit listeners on
            // the shared #modal-form, and a single OK fires the save once per prior
            // open — e.g. two accidental backdrop-dismisses then OK = three saves.
            dlg.addEventListener('close', () => { cleanup(); settle(null); }, { once: true });
            const onCancel = () => { cleanup(); settle(null); dlg.close(); };
            // Toggle the modal's busy state while an async save is in flight.
            const setPending = (on) => {
                const submit = $('#modal-submit');
                submit.disabled = on;
                submit.setAttribute('aria-busy', on ? 'true' : 'false');
                if (on) {
                    // Build from DOM nodes rather than an innerHTML string, so the
                    // label is never interpreted as markup (esc() only coerces to a
                    // string; the DOM's textContent is what makes this XSS-safe).
                    submit.textContent = '';
                    const spin = document.createElement('span');
                    spin.className = 'spin';
                    spin.setAttribute('aria-hidden', 'true');
                    submit.append(spin, document.createTextNode(' ' + submitLabel + '…'));
                } else submit.textContent = submitLabel;
                $('#modal-cancel').disabled = on;
                $('#modal-close').disabled = on;
            };
            const onSubmit = async (e) => {
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
                // settle() before dlg.close() so the close-event's settle(null) is a
                // no-op and callers still receive the submitted data.
                if (!save) { cleanup(); settle(data); dlg.close(); return; }
                // Async save: keep the modal open, show progress, surface errors inline.
                formErr.hidden = true;
                setPending(true);
                try {
                    await save(data);
                    cleanup(); settle(data); dlg.close();
                } catch (err) {
                    formErr.textContent = err.message || 'Speichern fehlgeschlagen';
                    formErr.hidden = false;
                    setPending(false);
                }
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
    // canClose: optional predicate; while it returns false the dialog cannot be
    // dismissed (cancel/close buttons, Escape, backdrop) — used to force an
    // acknowledgement before one-time content (e.g. backup codes) disappears.
    function contentModal(title, buildBody, { closeLabel = 'Schließen', canClose = null, hideCancel = false } = {}) {
        const dlg = $('#modal');
        $('#modal-title').textContent = title;
        $('#modal-submit').style.display = 'none';
        $('#modal-cancel').textContent = closeLabel;
        $('#modal-cancel').style.display = hideCancel ? 'none' : '';
        const body = $('#modal-body');
        body.innerHTML = '';
        // Consulted by the shared backdrop-click handler. Set fresh on every modal
        // open (contentModal here, formModal to null), so it is never stale while a
        // dialog is actually open.
        dlg._canClose = canClose;
        function onDismiss() { if (!canClose || canClose()) dlg.close(); }
        function onCancel(e) { if (canClose && !canClose()) e.preventDefault(); } // Escape
        function onClosed() {
            // Detach only THIS invocation's own listeners (per-invocation closures),
            // so a stale close event can neither remove the active modal's listeners
            // nor clobber its guard. The dialog's close event is async and can fire
            // after a newer modal has opened on the same element (e.g. setup2FA
            // closes then synchronously opens showBackupCodes); leaving _canClose
            // untouched keeps that newer modal's gate intact.
            $('#modal-cancel').removeEventListener('click', onDismiss);
            $('#modal-close').removeEventListener('click', onDismiss);
            dlg.removeEventListener('cancel', onCancel);
            dlg.removeEventListener('close', onClosed);
        }
        buildBody(body, () => dlg.close());
        $('#modal-cancel').addEventListener('click', onDismiss);
        $('#modal-close').addEventListener('click', onDismiss);
        dlg.addEventListener('cancel', onCancel);
        dlg.addEventListener('close', onClosed);
        dlg.showModal();
        return dlg;
    }

    // ---------- charts (pure SVG, interactive) ----------
    const MONTHS = ['J', 'F', 'M', 'A', 'M', 'J', 'J', 'A', 'S', 'O', 'N', 'D'];
    const REDUCE_MOTION = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
    const lastPositive = (vals) => { for (let i = vals.length - 1; i >= 0; i--) if (vals[i] > 0.0001) return i; return -1; };
    const tipNames = (labels) => (labels && labels.length === 12 ? MONTH_NAMES : (labels || []));
    const gid = () => 'c' + Math.random().toString(36).slice(2, 7);

    // Shared hover tooltip positioned over the chart (viewBox coords -> pixels).
    function chartTip(box, svg, W, H) {
        const tip = el('div', { class: 'c-tip' });
        box.append(tip);
        return {
            show: (vx, vy, html) => {
                const r = svg.getBoundingClientRect();
                tip.innerHTML = html;
                tip.style.left = (vx / W * r.width) + 'px';
                tip.style.top = (vy / H * r.height) + 'px';
                tip.style.opacity = 1;
            },
            hide: () => { tip.style.opacity = 0; },
        };
    }

    // Area/line chart with gradient fill, faint grid, hover crosshair + tooltip
    // and an emphasized latest point. Values are money; future (0) months aren't
    // drawn so the line stops at the latest activity.
    function chartLine(values, labels) {
        const W = 340, H = 160, pl = 8, pr = 8, pt = 16, pb = 22, n = values.length;
        const max = Math.max(1, ...values) * 1.12, iw = W - pl - pr, ih = H - pt - pb;
        const x = (i) => pl + (n <= 1 ? iw / 2 : i * iw / (n - 1));
        const y = (v) => pt + ih - (v / max) * ih;
        const hi = lastPositive(values), id = gid(), names = tipNames(labels);
        let line = '', area = '';
        if (hi >= 0) {
            const pts = [];
            for (let i = 0; i <= hi; i++) pts.push([x(i), y(values[i])]);
            line = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ');
            area = `${line} L ${x(hi).toFixed(1)} ${H - pb} L ${x(0).toFixed(1)} ${H - pb} Z`;
        }
        let grid = '';
        for (let r = 0; r <= 3; r++) { const gy = pt + ih * r / 3; grid += `<line class="c-grid" x1="${pl}" y1="${gy.toFixed(1)}" x2="${W - pr}" y2="${gy.toFixed(1)}"/>`; }
        let lab = '';
        if (labels) values.forEach((v, i) => { lab += `<text class="lbl" x="${x(i).toFixed(1)}" y="${H - pb + 13}" text-anchor="middle">${labels[i]}</text>`; });
        let endMark = '';
        if (hi >= 0) { const ex = x(hi), ey = y(values[hi]); endMark = `<circle class="c-endring" cx="${ex.toFixed(1)}" cy="${ey.toFixed(1)}" r="5.5"/><circle class="c-enddot" cx="${ex.toFixed(1)}" cy="${ey.toFixed(1)}" r="3.2"/>`; }
        const box = el('div', { class: 'chart' });
        box.innerHTML = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Verlauf">
          <defs>
            <linearGradient id="af-${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--chart-c1)" stop-opacity="0.32"/><stop offset="1" stop-color="var(--chart-c2)" stop-opacity="0"/></linearGradient>
            <linearGradient id="as-${id}" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="var(--chart-c1)"/><stop offset="1" stop-color="var(--chart-c2)"/></linearGradient>
          </defs>
          <g>${grid}</g>
          ${area ? `<path d="${area}" fill="url(#af-${id})"/>` : ''}
          ${line ? `<path class="c-line" style="stroke:url(#as-${id})" d="${line}"/>` : ''}
          <g>${lab}</g>
          <line class="c-cross" y1="${pt}" y2="${H - pb}"/>
          <circle class="c-focus" r="4"/>
          ${endMark}
          <rect class="c-overlay" x="0" y="0" width="${W}" height="${H}" fill="transparent"/></svg>`;
        const svg = box.querySelector('svg'), cross = box.querySelector('.c-cross'), focus = box.querySelector('.c-focus');
        const tip = chartTip(box, svg, W, H);
        if (hi >= 0 && !REDUCE_MOTION()) {
            const el2 = box.querySelector('.c-line');
            const len = el2.getTotalLength();
            el2.style.strokeDasharray = len; el2.style.strokeDashoffset = len;
            el2.getBoundingClientRect(); el2.style.transition = 'stroke-dashoffset .9s ease'; el2.style.strokeDashoffset = 0;
        }
        const overlay = svg.querySelector('.c-overlay');
        const at = (ev) => {
            if (hi < 0) return;
            const r = svg.getBoundingClientRect(); const px = (ev.clientX - r.left) / r.width * W;
            let i = Math.round((px - pl) / (iw / (n - 1 || 1))); i = Math.max(0, Math.min(hi, i));
            const cx = x(i), cy = y(values[i]);
            cross.setAttribute('x1', cx); cross.setAttribute('x2', cx); cross.style.opacity = 1;
            focus.setAttribute('cx', cx); focus.setAttribute('cy', cy); focus.style.opacity = 1;
            tip.show(cx, cy, `<span class="k">${names[i] || ''}</span><b>${eur(values[i])}</b>`);
        };
        const off = () => { cross.style.opacity = 0; focus.style.opacity = 0; tip.hide(); };
        overlay.addEventListener('pointermove', at);
        overlay.addEventListener('pointerdown', at);
        overlay.addEventListener('pointerleave', off);
        overlay.addEventListener('pointerup', off);
        return box;
    }

    // Vertical bars with rounded ends, gradient fill, a highlighted latest bar and
    // per-bar hover tooltip.
    function chartBars(values, labels) {
        const W = 340, H = 160, pl = 8, pr = 8, pt = 16, pb = 22, n = values.length;
        const max = Math.max(1, ...values) * 1.15, iw = W - pl - pr, ih = H - pt - pb;
        const gap = iw / n, bw = Math.min(24, gap * 0.62), hi = lastPositive(values), id = gid(), names = tipNames(labels);
        let grid = '';
        for (let r = 0; r <= 3; r++) { const gy = pt + ih * r / 3; grid += `<line class="c-grid" x1="${pl}" y1="${gy.toFixed(1)}" x2="${W - pr}" y2="${gy.toFixed(1)}"/>`; }
        let bars = '', lab = '';
        for (let i = 0; i < n; i++) {
            const h = (values[i] / max) * ih, bx = pl + i * gap + (gap - bw) / 2, by = H - pb - h;
            const on = i === hi;
            bars += `<rect class="c-bar${on ? ' on' : ''}" data-i="${i}" x="${bx.toFixed(1)}" y="${by.toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(0, h).toFixed(1)}" rx="4" fill="${on ? 'var(--accent)' : `url(#bf-${id})`}"/>`;
            if (labels) lab += `<text class="lbl" x="${(bx + bw / 2).toFixed(1)}" y="${H - pb + 13}" text-anchor="middle">${labels[i]}</text>`;
        }
        const box = el('div', { class: 'chart' });
        box.innerHTML = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Balken">
          <defs><linearGradient id="bf-${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--chart-c2)"/><stop offset="1" stop-color="var(--chart-c1)"/></linearGradient></defs>
          <g>${grid}</g><g>${bars}</g><g>${lab}</g></svg>`;
        const svg = box.querySelector('svg');
        const tip = chartTip(box, svg, W, H);
        const marks = svg.querySelectorAll('.c-bar');
        marks.forEach((b) => {
            const focus = () => {
                const i = +b.dataset.i;
                marks.forEach((o) => o.classList.toggle('dim', o !== b));
                tip.show(+b.getAttribute('x') + +b.getAttribute('width') / 2, +b.getAttribute('y'),
                    `<span class="k">${names[i] || ''}</span><b>${eur(values[i])}</b>`);
            };
            b.addEventListener('pointerenter', focus);
            b.addEventListener('pointerdown', focus);
        });
        svg.addEventListener('pointerleave', () => { tip.hide(); marks.forEach((o) => o.classList.remove('dim')); });
        return box;
    }

    // ---------- shared render helpers ----------
    // Empty-state illustration: a large muted stroke icon (by ICONS name) + text.
    const emptyState = (iconName, text) => el('div', { class: 'empty' }, el('span', { class: 'big' }, icon(iconName, 44)), text);
    // Line icons for stat tiles (inline SVG; CSP blocks external assets).
    const STAT_ICONS = {
        users: '<path d="M16 19a4 4 0 0 0-8 0"/><circle cx="12" cy="8" r="3.2"/>',
        warehouse: '<path d="M3 9l9-5 9 5v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V9z"/><path d="M9 20v-6h6v6"/>',
        car: '<rect x="3" y="10" width="18" height="6" rx="2"/><circle cx="7.5" cy="18" r="1.6"/><circle cx="16.5" cy="18" r="1.6"/><path d="M6 10l1.5-3h9L18 10"/>',
        tag: '<path d="M4 12V5h7l8 8-7 7-8-8z"/><circle cx="8.5" cy="8.5" r="1.2"/>',
        trend: '<path d="M4 15l5-5 3 3 6-7"/><path d="M16 6h3v3"/>',
        check: '<circle cx="12" cy="12" r="8.2"/><path d="M8.5 12.3l2.3 2.3 4.6-4.9"/>',
        clock: '<circle cx="12" cy="12" r="8.2"/><path d="M12 7.5V12l3 2"/>',
        receipt: '<path d="M6 3h12v18l-2.2-1.3-2 1.3-2-1.3-2 1.3-2-1.3L6 21V3Z"/><path d="M9.2 8.5h5.6M9.2 12h5.6"/>',
    };
    function statIcon(key) {
        const p = STAT_ICONS[key];
        if (!p) return null;
        const span = el('span', { class: 'stat-ic' });
        span.innerHTML = `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>`;
        return span;
    }
    // A KPI tile: big tabular value + uppercase label, an optional muted icon, and
    // an optional tone (teal/green/amber) that tints the value + a left accent bar.
    const stat = (value, label, opts = {}) => {
        const t = el('div', { class: 'stat' + (opts.tone ? ' tone-' + opts.tone : '') });
        const ic = statIcon(opts.icon);
        if (ic) t.append(ic);
        t.append(el('div', { class: 'value' }, esc(value)), el('div', { class: 'label' }, label));
        return t;
    };
    const personName = (p) => (`${p.first_name || ''} ${p.last_name || ''}`).trim() || '(ohne Namen)';
    const vehicleTitle = (v) => v.label || v.license_plate || v.category_name;
    const catById = (id) => state.categories.find((c) => c.id === Number(id));
    const STATUS_LABEL = { reserved: 'reserviert', stored: 'eingelagert', collected: 'abgeholt', cancelled: 'storniert' };
    const STATUS_COLOR = { stored: 'var(--success)', reserved: 'var(--warning)', collected: 'var(--text-muted)', cancelled: 'var(--danger)' };
    const MONTH_NAMES = ['Januar', 'Februar', 'März', 'April', 'Mai', 'Juni', 'Juli', 'August', 'September', 'Oktober', 'November', 'Dezember'];
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
            if (!slice.length) listEl.append(emptyState(opts.emptyIcon || 'box', opts.emptyText || 'Keine Einträge.'));
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

    // collapsibleRows renders the first `limit` items and tucks the rest behind a
    // "N weitere anzeigen" toggle, so long Zahlungs-/Rechnungslisten don't flood the
    // page. All data is already loaded — this is pure client-side reveal, no paging.
    function collapsibleRows(items, renderFn, limit = 5) {
        // margin-bottom matches a .card so the list (and its toggle button, which is
        // inline-flex and wouldn't space the next section on its own) keeps the normal
        // 0.75rem gap to whatever follows. Verified: 12px in the rendered layout.
        const wrap = el('div', { style: 'margin-bottom:.75rem' });
        items.slice(0, limit).forEach((it) => wrap.append(renderFn(it)));
        const rest = items.slice(limit);
        if (!rest.length) return wrap;
        const overflow = el('div', { style: 'display:none' });
        rest.forEach((it) => overflow.append(renderFn(it)));
        let open = false;
        const btn = el('button', { class: 'btn btn-ghost btn-sm btn-block', type: 'button', 'aria-expanded': 'false', style: 'margin-top:.4rem' });
        const label = () => { btn.textContent = open ? 'Weniger anzeigen' : `${rest.length} weitere anzeigen`; };
        btn.addEventListener('click', () => { open = !open; overflow.style.display = open ? '' : 'none'; btn.setAttribute('aria-expanded', String(open)); label(); });
        label();
        wrap.append(overflow, btn);
        return wrap;
    }

    // ================= ROUTER =================
    const routes = {};
    // Set before navigate('hall/<id>') to focus a spot on arrival (the hash router
    // can't carry a ?query, so we pass it out-of-band). The hall planner reads + clears it.
    let pendingSpotFocus = null;
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
    // Persistent <details>: remembers open/closed across re-renders (render()
    // rebuilds the DOM), so toggling something that triggers a refresh no longer
    // collapses an expanded dropdown. `key` must be stable for the same section.
    const detailsState = new Map();
    function persistDetails(key, cls, summaryText, defaultOpen) {
        const open = detailsState.has(key) ? detailsState.get(key) : !!defaultOpen;
        const det = el('details', { class: cls });
        if (open) det.open = true;
        det.append(el('summary', {}, summaryText));
        det.addEventListener('toggle', () => {
            detailsState.set(key, det.open);
            // Sliders mounted while the section was closed had zero width and
            // hid their thumb — measure them once the content is visible.
            if (det.open) $$('.seg-mini', det).forEach(placeThumb);
        });
        return det;
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
        try { await fn(page, id); window.scrollTo(0, 0); syncPageTitle(page); }
        catch (err) {
            page.innerHTML = '';
            page.append(el('div', { class: 'empty' }, 'Fehler: ' + err.message));
            syncPageTitle(page);
            if (err.status === 401) logout();
        }
    }
    // Mirror each route's leading heading into the visually-hidden page-level h1
    // so screen-reader heading navigation has a root. Layout is untouched.
    function syncPageTitle(page) {
        const h1 = $('#page-title');
        if (!h1) return;
        const lead = page.querySelector('.page-head h2, .page-head h3, .detail-head h2');
        h1.textContent = lead ? lead.textContent.trim() : 'Parkrr';
    }

    // ================= DASHBOARD =================
    // Selected dashboard year; null = current year (server default). Kept across
    // re-renders so slider toggles etc. don't reset the view.
    let dashYear = null;
    // Umsatz year-over-year compare mode, persisted: A = growth vs same window last
    // year, B = share of the full previous year, C = projected full-year vs last year.
    let yoyMode = (() => { try { return localStorage.getItem('parkrr-yoy') || 'A'; } catch (e) { return 'A'; } })();
    const YOY_MODE_TITLE = {
        A: 'Wachstum ggü. dem gleichen Zeitraum im Vorjahr',
        B: 'Anteil am kompletten Vorjahresumsatz (keine Wachstumsrate)',
        C: 'Hochrechnung aufs Gesamtjahr, verglichen mit dem Vorjahr gesamt',
    };
    // Fraction of the given year elapsed, aligned with the API's revenue numerator:
    // Overview accrues through until := now.AddDate(0,0,1) (now + 1 day, continuous),
    // so the projection denominator must use the same +1-day cutoff — otherwise the
    // year looks less elapsed than the numerator implies and the projection runs high.
    // 1 for a past year, ~0 for a future one.
    function yearElapsedFraction(year) {
        const now = new Date();
        const curYear = now.getUTCFullYear();
        if (year < curYear) return 1;
        if (year > curYear) return 0.01;
        const start = Date.UTC(year, 0, 1);
        const cutoff = now.getTime() + 86400000; // now + 1 day, matching the API cutoff
        const total = ((year % 4 === 0 && year % 100 !== 0) || year % 400 === 0) ? 366 : 365;
        return Math.min(Math.max((cutoff - start) / 86400000, 1) / total, 1);
    }
    // The Umsatz delta for the chosen compare mode. Returns {txt,cls,title,label}
    // or null when there's nothing sensible to compare.
    function yoyDelta(ov, mode) {
        const thisY = Number(ov.accrued_this_year) || 0;
        const prevW = Number(ov.accrued_prev_year) || 0; // same window last year
        const prevF = Number(ov.accrued_prev_full) || 0; // full previous calendar year
        const lbl = (suffix) => 'Umsatz ' + ov.year + ' · ' + suffix;
        if (mode === 'B') {
            if (prevF <= 0) return null;
            const share = Math.round((thisY / prevF) * 100);
            return { txt: share + ' %', cls: share >= 100 ? 'up' : 'flat',
                title: 'Anteil am kompletten Vorjahr (' + eur(prevF) + ') — keine Wachstumsrate', label: lbl('% vom Vorjahr') };
        }
        if (mode === 'C') {
            if (prevF <= 0) return null;
            const proj = thisY / yearElapsedFraction(ov.year);
            const pct = Math.round(((proj - prevF) / prevF) * 100);
            const txt = (pct > 999 ? '▲ >+999' : pct >= 0 ? '▲ +' + pct : '▼ ' + Math.abs(pct)) + ' %';
            return { txt, cls: pct > 0 ? 'up' : pct < 0 ? 'down' : 'flat',
                title: 'Hochrechnung ≈ ' + eur(proj) + ' ggü. Vorjahr gesamt (' + eur(prevF) + ')', label: lbl('Prognose ggü. ' + (ov.year - 1)) };
        }
        // Mode A (default): growth vs the same window last year. A tiny prior-year
        // base makes the ratio explode, so fall back to the absolute € gain (or
        // "neu" when there was no prior-year window at all).
        if (prevW <= 0) {
            return thisY > 0 ? { txt: '▲ neu', cls: 'up', title: 'kein Vorjahres-Zeitraum zum Vergleich', label: lbl('ggü. ' + (ov.year - 1)) } : null;
        }
        const pct = Math.round(((thisY - prevW) / prevW) * 100);
        const cls = pct > 0 ? 'up' : pct < 0 ? 'down' : 'flat';
        const txt = (pct > 999 || prevW < 0.10 * thisY)
            ? '▲ +' + eur(thisY - prevW)
            : pct >= 0 ? '▲ +' + pct + ' %' : '▼ ' + Math.abs(pct) + ' %';
        return { txt, cls, title: 'ggü. gleichem Zeitraum ' + (ov.year - 1), label: lbl('ggü. ' + (ov.year - 1)) };
    }
    routes.dashboard = async (page) => {
        const ov = await api.get('/overview' + (dashYear ? '?year=' + dashYear : ''));
        let occ = null;
        try { occ = await api.get('/occupancy'); } catch (e) { /* occupancy is best-effort */ }
        dashYear = ov.year;
        page.innerHTML = '';
        // Year switcher: browse past years; forward capped at the current year
        // (future years would be all zeros).
        const nowYear = new Date().getFullYear();
        const yearSel = el('div', { class: 'pager', style: 'margin:0' },
            el('button', { class: 'btn btn-ghost btn-sm', 'aria-label': 'Vorheriges Jahr', disabled: ov.year <= 2000,
                onclick: () => { dashYear = ov.year - 1; render(); } }, '‹'),
            el('span', { class: 'info' }, String(ov.year)),
            el('button', { class: 'btn btn-ghost btn-sm', 'aria-label': 'Nächstes Jahr', disabled: ov.year >= nowYear,
                onclick: () => { dashYear = ov.year + 1; render(); } }, '›'));
        page.append(el('div', { class: 'page-head' }, el('h2', {}, 'Übersicht'), yearSel));

        // Empty state for a fresh install: no persons yet -> onboard instead of
        // showing a wall of zeros and empty charts.
        if (ov.total_persons === 0) {
            const empty = el('div', { class: 'empty' },
                el('span', { class: 'big' }, icon('users', 44)),
                el('div', {}, 'Noch keine Daten. Lege deine erste Person an, dann füllt sich die Übersicht.'));
            if (canManage()) empty.append(el('button', { class: 'btn btn-primary', style: 'margin-top:1rem', onclick: () => navigate('persons') }, '+ Erste Person'));
            page.append(empty);
            return;
        }

        // Money first — "Offen gesamt" is the operator's lead metric, shown as a
        // gradient hero; Umsatz (with a YoY delta) and Bezahlt sit beside it.
        page.append(el('div', { class: 'dash-hero' },
            el('div', { class: 'dash-hero-label' }, 'Offen gesamt'),
            el('div', { class: 'dash-hero-num' }, eur(ov.outstanding_total)),
            el('div', { class: 'dash-hero-sub' }, 'Stand heute · offene Salden zum Nachfassen')));
        const umsatz = stat(eur(ov.accrued_this_year), 'Umsatz ' + ov.year, { icon: 'trend', tone: 'teal' });
        const yd = yoyDelta(ov, yoyMode);
        if (yd) {
            umsatz.querySelector('.value').append(el('span', { class: 'yoy ' + yd.cls, title: yd.title }, ' ' + yd.txt));
            umsatz.querySelector('.label').textContent = yd.label;
        }
        // "Eingegangen" = recorded payments (money-in log); surfaced next to Umsatz
        // once any payment exists, distinct from "Bezahlt" (the paid-flag total).
        const moneyRow = el('div', { class: 'stat-grid' }, umsatz);
        if ((Number(ov.payments_this_year) || 0) > 0 || (Number(ov.payments_total) || 0) > 0) {
            moneyRow.append(stat(eur(ov.payments_this_year), 'Eingegangen ' + ov.year, { icon: 'receipt', tone: 'teal' }));
        }
        moneyRow.append(stat(eur(ov.paid_total), 'Bezahlt', { icon: 'check', tone: 'green' }));
        page.append(moneyRow);
        // Compare-mode switch for the Umsatz delta — only when there is a prior year
        // to compare against. Choice is persisted across visits.
        if ((Number(ov.accrued_prev_year) || 0) > 0 || (Number(ov.accrued_prev_full) || 0) > 0) {
            const modeBtns = [['A', 'Zeitraum'], ['B', '% Vorjahr'], ['C', 'Prognose']].map(([m, lbl]) =>
                el('button', { class: 'yoy-mode' + (yoyMode === m ? ' active' : ''), type: 'button',
                    'aria-pressed': String(yoyMode === m), title: YOY_MODE_TITLE[m],
                    onclick: () => { yoyMode = m; try { localStorage.setItem('parkrr-yoy', m); } catch (e) { /* storage blocked */ } render(); } }, lbl));
            page.append(el('div', { class: 'yoy-modes', role: 'group', 'aria-label': 'Vergleichsmodus Umsatz' }, ...modeBtns));
        }
        page.append(el('div', { class: 'stat-grid' },
            stat(ov.total_persons, 'Personen', { icon: 'users' }),
            stat(ov.active_vehicles, 'aktiv eingestellt', { icon: 'warehouse' }),
            stat(ov.total_vehicles, 'Gefährte gesamt', { icon: 'car' }),
            stat(ov.total_categories, 'Tarife', { icon: 'tag' }),
        ));

        // Belegung — how many active Gefährte are positioned in a hall plan, plus a
        // per-hall breakdown. The plan is area-based (no fixed slots), so this is a
        // placement count, not "free slots".
        if (occ && occ.active > 0) {
            const occCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Belegung · im Plan platziert'));
            occCard.append(el('div', { class: 'stat-grid' },
                stat(occ.placed + ' / ' + occ.active, 'Gefährte platziert', { icon: 'warehouse', tone: 'teal' }),
                stat(Math.max(0, occ.active - occ.placed), 'noch nicht platziert', { icon: 'car' })));
            // UX4 — per-hall occupancy as a small proportional bar chart (inline SVG-free, CSP-safe).
            const halls = (occ.halls || []).filter((hh) => hh.placed > 0);
            const maxP = Math.max(1, ...halls.map((hh) => hh.placed || 0));
            halls.forEach((hh) => {
                occCard.append(el('div', { class: 'status-row dash-occrow' },
                    el('div', { class: 'status-name' }, esc(hh.name),
                        el('span', { class: 'muted', style: 'font-weight:400;font-size:.72rem;margin-left:.4rem' }, esc(hh.garage_name))),
                    el('div', { class: 'dash-bar', title: hh.placed + ' platziert' }, el('div', { class: 'dash-bar-fill', style: 'width:' + Math.round((hh.placed || 0) / maxP * 100) + '%' })),
                    el('div', { class: 'bar-val' }, String(hh.placed))));
            });
            page.append(occCard);
        }

        // Top open balances — turns the "Offen gesamt" number into an action
        // list: who to follow up with, one tap to their page.
        if (ov.top_outstanding && ov.top_outstanding.length) {
            const oweCard = el('div', { class: 'owe-card' });
            oweCard.append(el('div', { class: 'owe-head' },
                el('span', { class: 'sec-eyebrow' }, 'Top offene Salden'),
                el('span', { class: 'muted', style: 'font-size:.75rem' }, eur(ov.outstanding_total) + ' gesamt')));
            ov.top_outstanding.forEach((o, i) => {
                oweCard.append(el('button', { class: 'owe-row', onclick: () => navigate('persons/' + o.person_id),
                    'aria-label': o.name + ' öffnen · ' + eur(o.outstanding) + ' offen' },
                    el('span', { class: 'owe-nm' }, el('span', { class: 'owe-rank' }, i + 1), esc(o.name)),
                    el('span', { class: 'owe-amt' }, eur(o.outstanding), el('span', { class: 'owe-chev' }, '›'))));
            });
            page.append(oweCard);
        }

        // Überfällige Rechnungen — Mahnwesen light: wen mahnen, ein Tap zur Rechnung.
        let overdue = [];
        try { overdue = (await api.get('/invoices/overdue')) || []; } catch (e) { /* ignore */ }
        if (overdue.length) {
            const odCard = el('div', { class: 'owe-card' });
            const odTotal = overdue.reduce((s, o) => s + (Number(o.open_amount) || 0), 0);
            odCard.append(el('div', { class: 'owe-head' },
                el('span', { class: 'sec-eyebrow', style: 'color:var(--danger)' }, 'Überfällige Rechnungen'),
                el('span', { class: 'muted', style: 'font-size:.75rem' }, overdue.length + ' · ' + eur(odTotal) + ' offen')));
            overdue.slice(0, 6).forEach((o) => {
                odCard.append(el('a', { class: 'owe-row', href: '#/invoices/' + o.id, style: 'text-decoration:none' },
                    el('span', { class: 'owe-nm' }, esc(o.person_name), el('span', { class: 'muted', style: 'font-size:.72rem;margin-left:.4rem' }, 'Nr ' + esc(o.number) + ' · ' + o.days_overdue + ' Tg. überfällig')),
                    el('span', { class: 'owe-amt', style: 'color:var(--danger)' }, eur(o.open_amount), el('span', { class: 'owe-chev' }, '›'))));
            });
            page.append(odCard);
        }

        // Verträge, die auslaufen — end_date in den nächsten 30 Tagen, ein Tap zum
        // Gefährt (Vertrag verlängern / Status ändern).
        let ending = [];
        try { ending = (await api.get('/vehicles/ending-soon?days=30')) || []; } catch (e) { /* ignore */ }
        if (ending.length) {
            const enCard = el('div', { class: 'owe-card' });
            enCard.append(el('div', { class: 'owe-head' },
                el('span', { class: 'sec-eyebrow' }, 'Verträge laufen aus'),
                el('span', { class: 'muted', style: 'font-size:.75rem' }, ending.length + ' · nächste 30 Tage')));
            ending.slice(0, 6).forEach((e) => {
                const soon = e.days_left <= 7;
                enCard.append(el('a', { class: 'owe-row', href: '#/vehicles/' + e.id, style: 'text-decoration:none' },
                    el('span', { class: 'owe-nm' }, esc(e.label), el('span', { class: 'muted', style: 'font-size:.72rem;margin-left:.4rem' }, esc(e.person_name))),
                    el('span', { class: 'owe-amt', style: soon ? 'color:var(--danger)' : '' }, (e.days_left === 0 ? 'heute' : 'in ' + e.days_left + ' Tg.'), el('span', { class: 'owe-chev' }, '›'))));
            });
            page.append(enCard);
        }

        // Revenue chart
        const revCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Umsatz pro Monat · ' + ov.year));
        revCard.append(chartLine(ov.revenue_by_month, MONTHS));
        revCard.append(el('div', { class: 'legend' }, el('span', {}, el('span', { class: 'dotc', style: 'background:var(--primary)' }), 'Miete + Zusatzkosten')));
        page.append(revCard);

        // Extra charges per month
        const pcCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Zusatzkosten pro Monat · ' + ov.year));
        pcCard.append(chartBars(ov.charges_by_month, MONTHS));
        pcCard.append(el('div', { class: 'legend' }, el('span', {}, el('span', { class: 'dotc', style: 'background:var(--primary)' }), 'Zusatzkosten')));
        page.append(pcCard);

        // Status distribution
        const sc = ov.status_counts || {};
        const scCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Gefährte nach Status'));
        const maxS = Math.max(1, ...Object.values(sc));
        const bars = el('div', { class: 'bars' });
        for (const st of ['stored', 'reserved', 'collected', 'cancelled']) {
            const n = sc[st] || 0;
            const fill = el('div', { class: 'bar-fill', style: `background:${STATUS_COLOR[st]}` });
            bars.append(el('div', { class: 'status-row' },
                el('div', { class: 'status-name' }, el('span', { class: 'status-dot', style: `background:${STATUS_COLOR[st]}` }), STATUS_LABEL[st]),
                el('div', { class: 'bar-track' }, fill),
                el('div', { class: 'bar-val' }, String(n))));
            requestAnimationFrame(() => { fill.style.width = ((n / maxS) * 100) + '%'; });
        }
        scCard.append(bars);
        page.append(scCard);

        // CSV export for accounting — a plain download link carries the session
        // cookie; the server sends it as a ;-separated, BOM-prefixed attachment.
        const expLink = (entity, label) => el('a', { class: 'btn btn-ghost btn-sm', href: '/api/export/' + entity, download: '' }, '⭳ ' + label);
        page.append(el('div', { class: 'chart-card' },
            el('h3', {}, 'Export (CSV)'),
            el('div', { class: 'muted', style: 'font-size:.82rem;margin:-.35rem 0 .7rem' }, 'Für Buchhaltung/Steuerberater — öffnet direkt in Excel/LibreOffice.'),
            el('div', { class: 'btn-row', style: 'flex-wrap:wrap;gap:.5rem' },
                expLink('outstanding', 'Offene Posten'),
                expLink('payments', 'Zahlungen'),
                expLink('persons', 'Personen'),
                expLink('vehicles', 'Gefährte'))));
    };

    // ================= PERSONS =================
    routes.persons = async (page) => {
        await refreshLookups();
        // Open balance per person, batched in one request so each row can show its
        // settlement status (and a status-coloured rail) without an N+1 of stats.
        let oweMap = {};
        let oweOk = true;
        try { oweMap = (await api.get('/persons/outstanding')) || {}; }
        catch (e) { oweOk = false; toast('Salden konnten nicht geladen werden', 'error'); }
        // Overdue invoices → worst days-overdue per person, for a Fälligkeits-Badge.
        const overdueByPerson = {};
        try { ((await api.get('/invoices/overdue')) || []).forEach((o) => { overdueByPerson[o.person_id] = Math.max(overdueByPerson[o.person_id] || 0, o.days_overdue); }); }
        catch (e) { /* dashboard already surfaces overdue; ignore here */ }
        mountList(page, {
            title: 'Personen', emptyIcon: 'users', emptyText: 'Noch keine Personen.',
            onAdd: canManage() ? () => personForm() : null,
            items: state.persons,
            searchText: (p) => norm(personName(p) + ' ' + p.email + ' ' + p.phone),
            // Filter toggle: only persons with an open balance (Mahn-/Nachfass-Sicht).
            controls: (refresh, cs) => {
                const btn = el('button', { class: 'btn btn-ghost btn-sm', type: 'button', 'aria-pressed': 'false',
                    onclick: () => { cs.owedOnly = !cs.owedOnly; btn.className = 'btn btn-sm ' + (cs.owedOnly ? 'btn-primary' : 'btn-ghost'); btn.setAttribute('aria-pressed', String(!!cs.owedOnly)); refresh(); } },
                    'Nur offen');
                const out = [btn];
                // CSV bulk import (editor+): opens a dialog to download an example
                // file or pick a CSV. Round-trips the "Personen" export.
                if (canManage()) {
                    out.push(el('button', { class: 'btn btn-ghost btn-sm', type: 'button',
                        title: 'Personen aus CSV importieren', onclick: () => importPersonsDialog() }, icon('upload'), ' Import'));
                }
                return out;
            },
            extraFilter: (p, cs) => !cs.owedOnly || (Number(oweMap[p.id]) || 0) > 0.005,
            sorts: [
                { label: 'Name A–Z', cmp: (a, b) => personName(a).localeCompare(personName(b)) },
                { label: 'Name Z–A', cmp: (a, b) => personName(b).localeCompare(personName(a)) },
                { label: 'Offen zuerst', cmp: (a, b) => (Number(oweMap[b.id]) || 0) - (Number(oweMap[a.id]) || 0) },
                { label: 'Neueste zuerst', cmp: (a, b) => b.id - a.id },
            ],
            render: (p) => {
                // "known" = the person has billing activity, i.e. appears in the
                // outstanding map (has a Gefährt / Pauschale / Zusatzkosten). A person
                // with none isn't "Bezahlt" — there's simply nothing to settle — so
                // show a neutral row with no status chip. Also suppressed if the
                // balances failed to load. Amount/status + actions stack in a right rail.
                const known = oweOk && oweMap[p.id] !== undefined;
                const bal = Number(oweMap[p.id]) || 0;
                const owes = bal > 0.005;
                const stateCls = known ? (owes ? ' is-owed' : ' is-clear') : '';
                return el('div', { class: 'card pcard' + stateCls },
                    el('div', { class: 'card-row' },
                        el('div', { style: 'flex:1;cursor:pointer', onclick: () => navigate('persons/' + p.id) },
                            el('h3', {}, personName(p), ' ', p.has_flat_rate ? el('span', { class: 'badge badge-active', title: 'Pauschale' }, 'Pauschale') : null),
                            el('div', { class: 'card-meta' }, [p.email, p.phone].filter(Boolean).join(' · ') || 'keine Kontaktdaten')),
                        el('div', { class: 'row-side' },
                            known ? el('div', { class: 'pcard__status' },
                                owes
                                    ? el('span', { class: 'pcard__owe', title: 'Offener Saldo' }, eur(bal))
                                    : el('span', { class: 'pcard__paid', title: 'Keine offenen Beträge' }, 'Bezahlt'),
                                overdueByPerson[p.id] ? el('span', { class: 'badge', style: 'background:var(--danger);color:#fff;margin-top:.25rem', title: 'Überfällige Rechnung' }, overdueByPerson[p.id] + ' Tg. überfällig') : null) : null,
                            el('div', { class: 'card-actions' },
                                el('button', { class: 'btn btn-ghost btn-sm', 'aria-label': personName(p) + ' öffnen', onclick: () => navigate('persons/' + p.id) }, '›'),
                                canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: personName(p) + ' bearbeiten', 'aria-label': personName(p) + ' bearbeiten', onclick: () => personForm(p) }, icon('edit')),
                                canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: personName(p) + ' löschen', 'aria-label': personName(p) + ' löschen', onclick: (e) => delPerson(p, e.currentTarget.closest('.card')) }, icon('trash')),
                            ))));
            },
        });
    };

    async function personForm(existing) {
        await formModal({
            title: existing ? 'Person bearbeiten' : 'Neue Person',
            fields: [
                { name: 'first_name', label: 'Vorname', value: existing?.first_name },
                { name: 'last_name', label: 'Nachname', value: existing?.last_name },
                { name: 'email', label: 'E-Mail', type: 'email', value: existing?.email },
                { name: 'phone', label: 'Telefon', value: existing?.phone },
                { name: 'address', label: 'Adresse', type: 'textarea', value: existing?.address },
                { name: 'notes', label: 'Notizen', type: 'textarea', value: existing?.notes },
            ],
            save: async (data) => {
                if (existing) await api.put('/persons/' + existing.id, data);
                else await api.post('/persons', data);
                toast('Person gespeichert', 'success'); render();
            },
        });
    }
    function delPerson(p, node) {
        deleteWithUndo('Person löschen?', `„${personName(p)}“ und alle zugehörigen Gefährte werden gelöscht.`,
            () => api.del('/persons/' + p.id), () => render(), node);
    }

    // Import dialog: offer an example-file download or pick a CSV to import.
    async function importPersonsDialog() {
        const csvIn = el('input', { type: 'file', accept: '.csv,text/csv', style: 'display:none' });
        csvIn.addEventListener('change', async () => {
            const f = csvIn.files[0];
            csvIn.value = '';
            if (!f) return;
            const fd = new FormData();
            fd.append('file', f);
            let res;
            try {
                res = await api.upload('/import/persons', fd);
            } catch (e) {
                toast('Import fehlgeschlagen: ' + (e.message || e), 'error');
                return;
            }
            // Report the result first, then close — a close/render hiccup must not
            // be surfaced as an import failure.
            const parts = [res.imported + ' importiert'];
            if (res.skipped) parts.push(res.skipped + ' übersprungen');
            if (res.failed) parts.push(res.failed + ' fehlerhaft');
            toast(parts.join(' · '), res.failed ? 'error' : 'success');
            $('#modal-cancel').click();
            render();
        });
        await formModal({
            title: 'Personen importieren',
            submitLabel: 'Schließen',
            fields: [],
            onRender: (body) => {
                body.append(el('p', { class: 'muted', style: 'margin-top:0;font-size:.85rem' },
                    'CSV mit den Spalten vorname, nachname, email, telefon, adresse (optional notiz). Trennzeichen ; oder , — erste Zeile ist die Kopfzeile. Doppelte E-Mails und ungültige Zeilen werden übersprungen.'));
                body.append(el('div', { class: 'btn-row', style: 'gap:.5rem;flex-wrap:wrap;margin-top:.3rem' },
                    el('a', { class: 'btn btn-ghost btn-sm', href: '/api/import/persons/template', download: 'parkrr-personen-vorlage.csv' }, icon('download', 14), ' Beispiel-Datei'),
                    el('button', { type: 'button', class: 'btn btn-primary btn-sm', onclick: () => csvIn.click() }, icon('upload', 14), ' Datei wählen …'),
                    csvIn));
            },
            save: null,
        });
    }

    // Absolute, copy-paste-able URL: the backend returns an absolute link when
    // PARKRR_PUBLIC_BASE_URL is set, otherwise a relative "/#/portal/…" that we
    // resolve against the current origin so it can be shared as-is.
    const absLink = (link) => (link && link.startsWith('/')) ? location.origin + link : link;

    // Self-service access management for a person: create links (chosen duration,
    // optional e-mail), list all links with usage stats, revoke them singly or all.
    async function portalLinkFor(personId) {
        await formModal({
            title: 'Self-Service-Zugänge',
            submitLabel: 'Schließen',
            fields: [],
            onRender: async (body) => {
                // ---- create a new link ----
                const create = el('div', { class: 'card', style: 'padding:.75rem;margin-bottom:.6rem' },
                    el('div', { class: 'sec-eyebrow', style: 'margin-bottom:.4rem' }, 'Neuen Zugang erstellen'));
                const dur = el('select', { style: 'font:inherit;padding:.3rem .5rem' },
                    ...[[7, '7 Tage'], [30, '30 Tage'], [90, '90 Tage'], [180, '180 Tage'], [365, '1 Jahr']].map(([v, l]) => {
                        const o = el('option', { value: String(v) }, l); if (v === 30) o.selected = true; return o;
                    }));
                const ctl = el('div', { class: 'btn-row', style: 'gap:.6rem;flex-wrap:wrap;align-items:center' },
                    el('label', { style: 'font-size:.85rem' }, 'Gültigkeit ', dur));
                let sendChk = null;
                if (state.capabilities.mail) {
                    sendChk = el('input', { type: 'checkbox' });
                    ctl.append(el('label', { style: 'font-size:.85rem;display:inline-flex;align-items:center;gap:.3rem' }, sendChk, 'per E-Mail'));
                }
                const createBtn = el('button', { type: 'button', class: 'btn btn-primary btn-sm' }, '+ Link erzeugen');
                ctl.append(createBtn);
                const fresh = el('div', { style: 'margin-top:.5rem' });
                create.append(ctl, fresh);
                body.append(create);

                // ---- existing links ----
                body.append(el('div', { class: 'sec-eyebrow', style: 'margin:.2rem 0 .3rem' }, 'Bestehende Zugänge'));
                const listWrap = el('div');
                body.append(listWrap,
                    el('p', { class: 'muted', style: 'font-size:.78rem;margin-top:.5rem' },
                        'Der Link ist aus Sicherheitsgründen nur einmal beim Erstellen sichtbar. Bestehende Zugänge lassen sich einsehen und widerrufen.'));

                async function refresh() {
                    listWrap.innerHTML = '';
                    let links = [];
                    try { links = (await api.get('/persons/' + personId + '/portal-links')) || []; }
                    catch (e) { listWrap.append(el('p', { class: 'muted' }, 'Konnte nicht geladen werden.')); return; }
                    if (!links.length) { listWrap.append(el('p', { class: 'muted', style: 'font-size:.85rem' }, 'Noch keine Zugänge.')); return; }
                    links.forEach((l) => listWrap.append(portalLinkRow(l, refresh)));
                    if (links.filter((l) => l.status === 'aktiv').length > 1) {
                        listWrap.append(el('button', { type: 'button', class: 'btn btn-ghost btn-sm', style: 'margin-top:.4rem',
                            onclick: async () => { if (!await confirmDialog('Alle Zugänge widerrufen?', 'Alle aktiven Self-Service-Links dieser Person werden ungültig.', 'Widerrufen')) return; try { await api.post('/persons/' + personId + '/portal-link/revoke', {}); toast('Alle widerrufen', 'success'); refresh(); } catch (e) { toast(e.message, 'error'); } } }, 'Alle widerrufen'));
                    }
                }

                createBtn.onclick = async () => {
                    createBtn.disabled = true;
                    try {
                        const send = !!(sendChk && sendChk.checked);
                        const r = await api.post('/persons/' + personId + '/portal-link', { days: Number(dur.value), send });
                        const url = absLink(r.link);
                        fresh.innerHTML = '';
                        const inp = el('input', { type: 'text', readonly: 'readonly', value: url, style: 'width:100%;font-size:.8rem', onclick: (e) => e.currentTarget.select() });
                        fresh.append(el('label', { style: 'font-size:.8rem' }, 'Neuer Link (nur jetzt sichtbar)'), inp,
                            el('button', { type: 'button', class: 'btn btn-ghost btn-sm', style: 'margin-top:.35rem',
                                onclick: async () => { try { await navigator.clipboard.writeText(url); toast('Link kopiert', 'success'); } catch (_) { inp.select(); toast('Bitte manuell kopieren', 'error'); } } }, 'Kopieren'));
                        if (send) toast(r.emailed ? 'Link erstellt & per E-Mail gesendet' : (r.has_email ? 'Link erstellt · E-Mail nicht gesendet (Basis-URL fehlt?)' : 'Link erstellt · kein E-Mail-Kontakt'), r.emailed ? 'success' : 'error');
                        else toast('Link erstellt', 'success');
                        refresh();
                    } catch (e) { toast(e.message || 'Fehler', 'error'); }
                    finally { createBtn.disabled = false; }
                };

                await refresh();
            },
            save: null,
        });
    }

    function portalLinkRow(l, refresh) {
        const created = new Date(l.created_at).toLocaleDateString('de-DE');
        const exp = new Date(l.expires_at).toLocaleDateString('de-DE');
        const used = l.last_used_at ? ('zuletzt ' + fmtDateTime(l.last_used_at)) : 'nie genutzt';
        const badge = el('span', { class: 'badge' + (l.status === 'aktiv' ? ' badge-active' : ''),
            style: l.status === 'aktiv' ? '' : 'background:var(--muted);color:#fff' }, l.status);
        return el('div', { class: 'pay-row card' },
            el('div', { class: 'pay-main' },
                el('div', { class: 'pay-method' }, 'gültig bis ' + exp, ' ', badge),
                el('div', { class: 'pay-date' }, 'erstellt ' + created + ' · ' + used + (l.created_by ? ' · ' + esc(l.created_by) : ''))),
            (l.status === 'aktiv') ? el('button', { class: 'btn btn-ghost btn-sm', title: 'Widerrufen', 'aria-label': 'Zugang widerrufen',
                onclick: async () => { if (!await confirmDialog('Zugang widerrufen?', 'Dieser Link wird sofort ungültig.', 'Widerrufen')) return; try { await api.post('/portal-links/' + l.id + '/revoke', {}); toast('Widerrufen', 'success'); refresh(); } catch (e) { toast(e.message, 'error'); } } }, 'Widerrufen') : null);
    }

    // ---------- PERSON DETAIL ----------
    routes.person = async (page, id) => {
        await refreshLookups();
        const stats = await api.get('/persons/' + id + '/stats');
        // Load all billing reads together and let a failure propagate to the route's
        // error handling — a failed read must not masquerade as "no data" (which would
        // hide real records and invite writes against a wrong picture). `|| []` only
        // covers an empty body, never an error.
        const [vehicles, charges, paymentsR, invoicesR] = await Promise.all([
            api.get('/vehicles?person_id=' + id), api.get('/charges?person_id=' + id),
            api.get('/persons/' + id + '/payments'), api.get('/persons/' + id + '/invoices'),
        ]);
        const payments = paymentsR || [];
        const invoices = invoicesR || [];
        const recs = stats.recurring_charges || [];
        // Per-vehicle summary of bound extra costs (one-off total + recurring
        // accrued), surfaced on the vehicle cards and their Pauschale nesting.
        const chargeByVeh = {};
        const addBoundCharge = (vid, amt) => {
            if (vid == null) return;
            const e = chargeByVeh[vid] || (chargeByVeh[vid] = { sum: 0, count: 0 });
            e.sum += Number(amt) || 0; e.count += 1;
        };
        charges.forEach((c) => addBoundCharge(c.vehicle_id, c.total));
        recs.forEach((rc) => addBoundCharge(rc.vehicle_id, rc.accrued));
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', onclick: () => navigate('persons'), 'aria-label': 'Zurück' }, '‹'),
            el('h2', { style: 'margin:0;flex:1' }, stats.person_name || 'Person'),
            canManage() ? el('button', { class: 'btn btn-ghost btn-sm', title: 'Read-only Kundenzugang (Magic-Link)', onclick: () => portalLinkFor(id) }, icon('mail', 15), ' Self-Service') : null));

        // Balance card: lead with the one number the user came for (the open
        // balance) as a hero, then the derivation as a quiet breakdown.
        const owed = stats.balance > 0.005;
        page.append(el('div', { class: 'card' },
            el('div', { class: 'bal-hero-label' }, 'Offener Saldo'),
            el('div', { class: 'bal-hero-num ' + (owed ? 'is-owed' : 'is-clear') }, eur(stats.balance)),
            el('div', { class: 'bal-hero-sub' }, 'Stand heute · Aufgelaufen − Bezahlt')));
        // Derivation lives in its own card below the hero: labelled figures that add
        // up to the hero exactly. USt (only when invoiced) and per-period Pauschale
        // payments (only when used) appear so the small ledger always reconciles.
        const figs = [
            el('div', { class: 'fig' }, el('div', { class: 'fig-l' }, 'Miete'), el('div', { class: 'fig-v' }, eur(stats.total_accrued))),
            el('div', { class: 'fig' }, el('div', { class: 'fig-l' }, 'Zusatzkosten'), el('div', { class: 'fig-v' }, eur(stats.total_charges))),
        ];
        if ((stats.invoiced_tax || 0) > 0.005) {
            figs.push(el('div', { class: 'fig' }, el('div', { class: 'fig-l' }, 'USt (fakturiert)'), el('div', { class: 'fig-v' }, eur(stats.invoiced_tax))));
        }
        figs.push(el('div', { class: 'fig' }, el('div', { class: 'fig-l' }, 'Bezahlt'), el('div', { class: 'fig-v is-paid' }, eur(stats.total_paid))));
        if ((stats.period_paid || 0) > 0.005) {
            figs.push(el('div', { class: 'fig' }, el('div', { class: 'fig-l' }, 'Pauschale-Zahlung'), el('div', { class: 'fig-v is-paid' }, eur(stats.period_paid))));
        }
        page.append(el('div', { class: 'card bal-figs' }, ...figs));

        // flat-rate agreements (Pauschalen). Per-vehicle coverage badges/payment
        // come from the backend (v.flat_rate_covered / v.uncovered_cost).
        const ags = stats.agreements || [];
        if (canBill() || ags.length) {
            // A Pauschale only moves to the archive once it has ended AND is fully
            // paid — an ended-but-unpaid agreement stays visible so the open payment
            // isn't hidden. "Fully paid" uses the same invoice-aware state as the
            // progress bar (agreementPaid().done), so a Pauschale settled through a
            // paid Rechnung archives too — not only ones cleared via the paid flags.
            const ended = (a) => agreementPaid(a).done && a.end_date && a.end_date.slice(0, 10) <= today();
            const activeAg = ags.filter((a) => !ended(a));
            const endedAg = ags.filter(ended);
            const frCard = el('div', { class: 'card' });
            // Badge counts the rows actually shown here (active); ended ones sit in
            // their own "Beendete Pauschalen" section with its own count.
            frCard.append(el('div', { class: 'card-row', style: 'align-items:baseline' },
                el('div', { class: 'sec-group' },
                    el('h3', { class: 'sec-eyebrow' }, 'Pauschalen'),
                    activeAg.length ? el('span', { class: 'sec-count' }, activeAg.length) : null),
                canBill() ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => agreementForm(id, vehicles) }, '+ Pauschale') : null));
            if (!ags.length) frCard.append(el('div', { class: 'card-meta', style: 'margin-top:.3rem' }, 'Keine Pauschale – Abrechnung je Gefährt.'));
            else activeAg.forEach((a) => frCard.append(agreementRow(id, a, vehicles, chargeByVeh)));
            if (activeAg.length === 0 && ags.length) frCard.append(el('div', { class: 'card-meta', style: 'margin-top:.3rem' }, 'Keine laufende Pauschale.'));
            if (endedAg.length) {
                const det = persistDetails('person-ag-archive-' + id, 'archive-section', 'Beendete Pauschalen (' + endedAg.length + ')', false);
                endedAg.forEach((a) => det.append(agreementRow(id, a, vehicles, chargeByVeh)));
                frCard.append(det);
            }
            page.append(frCard);
        }

        // Standalone vehicles only — vehicles bound to a Pauschale are shown nested
        // under their Pauschale above, not here. Active first; archived collapsed.
        const standalone = vehicles.filter((v) => !v.flat_rate_covered);
        const activeVeh = standalone.filter((v) => !v.archived);
        const archivedVeh = standalone.filter((v) => v.archived);
        const vh = el('div', { class: 'page-head section-head' },
            el('div', { class: 'sec-group' }, el('h3', { class: 'sec-eyebrow' }, 'Einzelne Gefährte'),
                activeVeh.length ? el('span', { class: 'sec-count' }, activeVeh.length) : null));
        if (canManage()) vh.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => vehicleForm(null, id) }, '+ Gefährt'));
        page.append(vh);
        if (!activeVeh.length) page.append(el('p', { class: 'muted' }, 'Keine einzeln abgerechneten Gefährte.'));
        else activeVeh.forEach((v) => page.append(vehicleCard(v, { linkable: true, chargeInfo: chargeByVeh[v.id] })));
        if (archivedVeh.length) {
            const det = persistDetails('person-archive-' + id, 'archive-section', 'Archiv (' + archivedVeh.length + ')', false);
            archivedVeh.forEach((v) => det.append(vehicleCard(v, { linkable: true, chargeInfo: chargeByVeh[v.id] })));
            page.append(det);
        }

        // Zusatzkosten: one section for both — recurring as accruing cards on top,
        // one-off as rows below. The "+ Position" dialog chooses the billing type.
        const chCount = charges.length + recs.length;
        const ch = el('div', { class: 'page-head section-head' },
            el('div', { class: 'sec-group' }, el('h3', { class: 'sec-eyebrow' }, 'Zusatzkosten'),
                chCount ? el('span', { class: 'sec-count' }, chCount) : null));
        if (canBill()) ch.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => chargeForm(id) }, '+ Position'));
        page.append(ch);
        if (!chCount) page.append(el('p', { class: 'muted' }, 'Keine Zusatzkosten.'));
        else {
            recs.forEach((rc) => page.append(recurringRow(id, rc)));
            if (charges.length) page.append(financeList(charges));
        }

        // Zahlungen (recorded money-in). A dedicated log + Kontoauszug, separate
        // from the per-item "bezahlt" toggle above (which drives the Saldo).
        const activePayments = payments.filter((p) => !p.reversed).length; // count excludes storniert, like the money figures
        const pz = el('div', { class: 'page-head section-head' },
            el('div', { class: 'sec-group' }, el('h3', { class: 'sec-eyebrow' }, 'Zahlungen'),
                activePayments ? el('span', { class: 'sec-count' }, activePayments) : null));
        if (canBill()) pz.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => paymentForm(id, stats) }, '+ Zahlung'));
        page.append(pz);
        const kontoCard = el('div', { class: 'card konto' },
            kontoFig('Eingegangen ' + stats.year, eur(stats.payments_year), 'in'),
            kontoFig('Eingegangen gesamt', eur(stats.payments_total), ''));
        if ((Number(stats.credit) || 0) > 0.005) {
            kontoCard.append(kontoFig('Guthaben', eur(stats.credit), 'in'));
        }
        page.append(kontoCard);
        if ((Number(stats.credit) || 0) > 0.005 && canBill()) {
            page.append(el('div', { class: 'card-row', style: 'margin:-.3rem 0 .2rem;align-items:center;gap:.5rem' },
                el('div', { class: 'card-meta', style: 'flex:1' }, 'Guthaben ' + eur(stats.credit) + ' verfügbar.'),
                el('button', { class: 'btn btn-ghost btn-sm', onclick: (e) => applyCredit(id, e.currentTarget) }, 'Guthaben anrechnen')));
        }
        if (!payments.length) page.append(el('p', { class: 'muted' }, 'Noch keine Zahlungen erfasst.'));
        else page.append(collapsibleRows(payments, (p) => paymentRow(p)));

        // Rechnungen (fortlaufend nummeriert, unveränderlich)
        const rz = el('div', { class: 'page-head section-head' },
            el('div', { class: 'sec-group' }, el('h3', { class: 'sec-eyebrow' }, 'Rechnungen'),
                invoices.length ? el('span', { class: 'sec-count' }, invoices.length) : null));
        if (canBill()) rz.append(el('button', { class: 'btn btn-primary btn-sm', onclick: (e) => createInvoiceFor(id, e.currentTarget) }, '+ Rechnung'));
        page.append(rz);
        if (!invoices.length) page.append(el('p', { class: 'muted' }, 'Noch keine Rechnungen. „+ Rechnung" erstellt eine aus den offenen Einzelposten.'));
        else page.append(collapsibleRows(invoices, (iv) => invoiceRow(iv)));

        // statistics at the bottom, below the actionable sections
        const chartCard = el('div', { class: 'chart-card' }, el('h3', {}, 'Kosten pro Monat · ' + stats.year));
        chartCard.append(chartBars(stats.monthly_accrued, MONTHS));
        page.append(chartCard);
        if (stats.years.length) {
            const yc = el('div', { class: 'chart-card' }, el('h3', {}, 'Kosten pro Jahr'));
            const max = Math.max(...stats.years.map((y) => y.cost), 1);
            // Look up each year's predecessor to show a year-over-year delta.
            const byYear = {};
            stats.years.forEach((y) => { byYear[y.year] = y.cost; });
            const bars = el('div', { class: 'bars' });
            for (const y of stats.years) {
                const prior = byYear[y.year - 1];
                let yoy = null, yoyCls = '';
                if (prior != null && prior > 0) {
                    const pct = Math.round(((y.cost - prior) / prior) * 100);
                    if (pct > 0) { yoy = '▲ +' + pct + ' %'; yoyCls = 'up'; }
                    else if (pct < 0) { yoy = '▼ ' + Math.abs(pct) + ' %'; yoyCls = 'down'; }
                    else { yoy = '± 0 %'; yoyCls = 'flat'; }
                }
                bars.append(el('div', { class: 'bar-row' },
                    el('div', {}, String(y.year)),
                    el('div', { class: 'bar-track' }, el('div', { class: 'bar-fill', style: `width:${(y.cost / max) * 100}%` })),
                    el('div', { class: 'bar-val' }, eur(y.cost),
                        yoy ? el('span', { class: 'yoy ' + yoyCls, title: 'Veränderung gegenüber ' + (y.year - 1) }, yoy) : null)));
            }
            yc.append(bars);
            page.append(yc);
        }
    };

    // ---------- Payments (Zahlungen / Kontoauszug) ----------
    const PAY_METHODS = [{ v: 'bar', l: 'Bar' }, { v: 'ueberweisung', l: 'Überweisung' }, { v: 'paypal', l: 'PayPal' }, { v: 'sonstiges', l: 'Sonstiges' }];
    const payMethodLabel = (m) => (PAY_METHODS.find((x) => x.v === m) || { l: m }).l;
    function kontoFig(label, value, tone) {
        return el('div', { class: 'konto-fig' + (tone ? ' tone-' + tone : '') },
            el('div', { class: 'konto-v' }, value),
            el('div', { class: 'konto-l' }, label));
    }
    // attrText names a position with its kind context: a Gefährt line reads as rent
    // ("… · Miete"); the other kinds already carry it in the label.
    function attrText(it) {
        return it.kind === 'vehicle' ? it.label + ' · Miete' : it.label;
    }
    // attrHeadline is the one-line summary of what a payment settles: the distinct
    // position labels, capped so the collapsed row stays short.
    function attrHeadline(items) {
        const labels = [...new Set(items.map((i) => i.label))];
        if (labels.length <= 2) return labels.join(' · ');
        return labels.slice(0, 2).join(' · ') + ' · +' + (labels.length - 2);
    }
    // attrDetails is the Variant-D collapsible (only used for ≥2 positions): a summary
    // line that expands to the full breakdown with per-position amount.
    function attrDetails(items) {
        const det = el('details', { class: 'pay-attr' });
        det.append(el('summary', {}, el('span', { class: 'caret' }, '▸'), el('span', {}, attrHeadline(items))));
        const list = el('div', { class: 'attr-list' });
        for (const it of items) {
            list.append(el('div', { class: 'attr-row' },
                el('div', { class: 'attr-lab' }, esc(attrText(it)),
                    it.period ? el('div', { class: 'attr-per' }, esc(it.period)) : null),
                (Number(it.amount) || 0) > 0.005 ? el('div', { class: 'attr-amt' }, eur(it.amount)) : el('span', {})));
        }
        det.append(list);
        return det;
    }
    function paymentRow(p) {
        const meta = new Date(p.paid_on).toLocaleDateString('de-DE') + (p.note ? ' · ' + p.note : '')
            + (p.reversed ? ' · storniert' : '');
        const head = el('div', { class: 'pay-head' },
            el('div', { class: 'pay-main' },
                el('div', { class: 'pay-method' }, payMethodLabel(p.method)),
                el('div', { class: 'pay-date' }, meta)),
            el('div', { class: 'pay-amt' }, eur(p.amount)));
        // A reversed payment is kept for the record (BAO) — no further action on it.
        if (canBill() && !p.reversed) {
            head.append(el('button', { class: 'btn btn-ghost btn-sm', 'aria-label': 'Zahlung stornieren', title: 'Zahlung stornieren', onclick: (e) => delPayment(p, e.currentTarget.closest('.card')) }, icon('trash', 15)));
        }
        const card = el('div', { class: 'card pay-card' + (p.reversed ? ' is-reversed' : '') }, head);
        // What the payment settled. One position → a compact line (no redundant
        // expand, amount already in the header); several → the collapsible breakdown.
        const items = p.items || [];
        if (items.length === 1) {
            const it = items[0];
            card.append(el('div', { class: 'pay-attr1' }, attrText(it) + (it.period ? ' · ' + it.period : '')));
        } else if (items.length > 1) {
            card.append(attrDetails(items));
        }
        return card;
    }
    function delPayment(p, node) {
        // BAO: the payment is not deleted but reversed (Storno) — the record is kept.
        deleteWithUndo('Zahlung stornieren?',
            eur(p.amount) + ' vom ' + new Date(p.paid_on).toLocaleDateString('de-DE') + ' wird storniert (Datensatz bleibt erhalten).',
            () => api.del('/payments/' + p.id), () => render(), node);
    }
    async function paymentForm(personId, stats) {
        const open = Math.max(0, Number(stats && stats.balance) || 0);
        let items = [];
        try { items = (await api.get('/persons/' + personId + '/open-items')) || []; } catch (e) { /* keep empty */ }
        // Selection state: default = automatic (oldest first), everything ticked.
        const sel = { auto: true, checked: new Set(items.map((i) => i.kind + ':' + i.id)) };

        await formModal({
            title: 'Zahlung erfassen',
            fields: [
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', required: true, value: open ? open.toFixed(2) : '', help: open ? 'Vorausgefüllt: offener Saldo – für Teil-/Vorauszahlung anpassen.' : '' },
                { name: 'paid_on', label: 'Datum', type: 'date', value: today() },
                { name: 'method', label: 'Methode', type: 'select', value: 'bar', options: PAY_METHODS.map((m) => ({ value: m.v, label: m.l })) },
                { name: 'note', label: 'Notiz (optional)', value: '' },
            ],
            onRender: (body) => {
                segmentedField(body, 'method', PAY_METHODS);
                const amountEl = () => body.querySelector('#f_amount');
                const box = el('div', { class: 'alloc-box' });
                const foot = el('div', { class: 'alloc-foot' });
                const list = el('div', { class: 'alloc-list' });

                const refresh = () => {
                    const amt = Number(amountEl().value) || 0;
                    let selTotal = 0;
                    items.forEach((it) => { if (sel.checked.has(it.kind + ':' + it.id)) selTotal += Number(it.owed) || 0; });
                    list.style.display = sel.auto ? 'none' : '';
                    const covered = sel.auto ? Math.min(amt, items.reduce((s, i) => s + (Number(i.owed) || 0), 0)) : Math.min(amt, selTotal);
                    const credit = Math.max(0, amt - covered);
                    foot.innerHTML = '';
                    foot.append(el('span', {}, sel.auto ? 'Automatisch – älteste zuerst' : ('Zugeordnet ' + eur(covered))));
                    foot.append(el('span', { class: credit > 0.005 ? 'is-credit' : '' }, credit > 0.005 ? ('Guthaben ' + eur(credit)) : 'Rest ' + eur(0)));
                };

                const autoChk = el('input', { type: 'checkbox' }); autoChk.checked = true;
                autoChk.addEventListener('change', () => { sel.auto = autoChk.checked; refresh(); });
                box.append(el('label', { class: 'switch alloc-auto' }, autoChk, el('span', { class: 'track' }), el('span', {}, 'Automatisch – offene Posten älteste zuerst begleichen')));

                if (!items.length) {
                    box.append(el('div', { class: 'card-meta', style: 'margin:.3rem 0' }, 'Keine offenen Einzelposten – der Betrag wird als Guthaben verbucht.'));
                } else {
                    items.forEach((it) => {
                        const key = it.kind + ':' + it.id;
                        const cb = el('input', { type: 'checkbox' }); cb.checked = true;
                        cb.addEventListener('change', () => { if (cb.checked) sel.checked.add(key); else sel.checked.delete(key); refresh(); });
                        list.append(el('label', { class: 'alloc-item' }, cb,
                            el('span', { class: 'ai-m' }, esc(it.label)),
                            el('span', { class: 'ai-a' }, eur(it.owed))));
                    });
                    box.append(list);
                }
                box.append(foot);
                body.append(box);
                amountEl().addEventListener('input', refresh);
                refresh();
            },
            save: async (data) => {
                if (!(Number(data.amount) > 0)) { toast('Betrag muss größer als 0 sein', 'error'); throw new Error('invalid amount'); }
                const payload = { amount: Number(data.amount), paid_on: data.paid_on, method: data.method, note: data.note || '' };
                if (sel.auto) payload.allocate = true;
                else payload.allocations = [...sel.checked].map((k) => { const [kind, id] = k.split(':'); return { kind, id: Number(id) }; });
                const res = await api.post('/persons/' + personId + '/payments', payload);
                const n = res && res.settled;
                toast(n ? `Zahlung erfasst · ${n} Posten abgestempelt` : 'Zahlung erfasst', 'success'); render();
            },
        });
    }
    async function applyCredit(personId, btn) {
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Rechne an …';
        try {
            const r = await api.post('/persons/' + personId + '/apply-credit', {});
            const n = r && r.settled;
            toast(n ? `Guthaben angerechnet · ${n} Posten abgestempelt` : 'Kein offener Posten zum Anrechnen', n ? 'success' : 'error');
            render();
        } catch (e) { toast(e.message, 'error'); btn.disabled = false; btn.textContent = o; }
    }

    // ---------- Rechnungen (invoices) ----------
    const fmtQty = (q) => Number(q).toLocaleString('de-DE', { maximumFractionDigits: 2 });
    const fmtRate = (r) => Number(r).toLocaleString('de-DE', { maximumFractionDigits: 2 });
    const INV_STATUS = { offen: ['offen', 'warn'], teilbezahlt: ['teilbezahlt', 'warn'], bezahlt: ['bezahlt', 'ok'], storniert: ['storniert', 'muted'], storno: ['Storno', 'muted'] };
    function invStatusBadge(iv) {
        const [label, tone] = INV_STATUS[iv.status] || [iv.status || '', 'muted'];
        return el('span', { class: 'inv-badge ' + tone }, label);
    }
    // invSummary groups an invoice's billed positions by label with their periods:
    // "Wohnwagen (2025, 2026) · Frostschutz-Check".
    function invSummary(positions) {
        const byLabel = new Map();
        for (const p of positions) {
            if (!byLabel.has(p.label)) byLabel.set(p.label, new Set());
            if (p.period) byLabel.get(p.label).add(p.period);
        }
        return [...byLabel].map(([label, pers]) => label + (pers.size ? ' (' + [...pers].join(', ') + ')' : '')).join(' · ');
    }
    function invoiceRow(iv) {
        const neg = iv.status === 'storno' || Number(iv.total) < 0;
        const main = el('div', { class: 'pay-main' },
            el('div', { class: 'pay-method' }, 'Rechnung ' + esc(iv.number), ' ', invStatusBadge(iv)),
            el('div', { class: 'pay-date' }, new Date(iv.issued_on).toLocaleDateString('de-DE')
                + (iv.kleinunternehmer ? '' : ' · inkl. USt')
                + (iv.status === 'teilbezahlt' ? ' · offen ' + eur(iv.open_amount) : '')));
        // What the invoice bills (Gefährt/Pauschale · Periode) — one muted line; the
        // card links to the invoice page for the full positions.
        if ((iv.positions || []).length) {
            main.append(el('div', { class: 'inv-bills' }, 'berechnet: ' + invSummary(iv.positions)));
        }
        return el('a', { class: 'card pay-row inv-link', href: '#/invoices/' + iv.id }, main,
            el('div', { class: 'pay-amt', style: neg ? 'color:var(--danger)' : 'color:var(--text)' }, eur(iv.total)));
    }
    async function payInvoiceFor(iv) {
        await formModal({
            title: 'Rechnung ' + iv.number + ' bezahlen',
            fields: [
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', required: true, value: Number(iv.open_amount).toFixed(2), help: 'Offen: ' + eur(iv.open_amount) + ' – für Teilzahlung anpassen.' },
                { name: 'paid_on', label: 'Datum', type: 'date', value: today() },
                { name: 'method', label: 'Methode', type: 'select', value: 'bar', options: PAY_METHODS.map((m) => ({ value: m.v, label: m.l })) },
            ],
            onRender: (body) => segmentedField(body, 'method', PAY_METHODS),
            save: async (d) => {
                if (!(Number(d.amount) > 0)) { toast('Betrag muss größer als 0 sein', 'error'); throw new Error('invalid amount'); }
                const r = await api.post('/persons/' + iv.person_id + '/pay-invoices', {
                    amount: Number(d.amount), paid_on: d.paid_on, method: d.method,
                    allocations: [{ invoice_id: iv.id, amount: Number(d.amount) }],
                });
                toast((r && r.unallocated > 0.005) ? ('Bezahlt · Rest ' + eur(r.unallocated) + ' nicht zugeordnet') : 'Rechnung bezahlt', 'success');
                render();
            },
        });
    }
    async function stornoInvoice(iv) {
        if (!await confirmDialog('Rechnung stornieren?', 'Erstellt einen unveränderlichen Storno-Gegenbeleg (§ Unveränderbarkeit). Die abgerechneten Positionen werden wieder fakturierbar.', 'Stornieren')) return;
        try {
            const st = await api.post('/invoices/' + iv.id + '/cancel', {});
            toast('Storniert · Gegenbeleg ' + st.number, 'success');
            location.hash = '#/invoices/' + st.id;
        } catch (e) { toast(e.message, 'error'); }
    }
    async function remindInvoice(iv) {
        if (!await confirmDialog('Zahlungserinnerung senden?',
            'Sendet eine E-Mail mit den offenen Rechnungsdaten an den hinterlegten Kontakt der Person.', 'Senden')) return;
        try {
            const r = await api.post('/invoices/' + iv.id + '/remind', {});
            toast('Erinnerung gesendet' + (r && r.to ? ' an ' + r.to : ''), 'success');
        } catch (e) { toast(e.message || 'Senden fehlgeschlagen', 'error'); }
    }
    async function createInvoiceFor(personId, btn) {
        if (!await confirmDialog('Rechnung erstellen?', 'Erstellt eine fortlaufend nummerierte Rechnung aus allen offenen Einzelposten (Gefährte + Einmal-Zusatzkosten). Rechnungen sind unveränderlich.', 'Erstellen')) return;
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Erstelle …';
        try {
            const iv = await api.post('/persons/' + personId + '/invoices', {});
            toast('Rechnung ' + iv.number + ' erstellt', 'success');
            location.hash = '#/invoices/' + iv.id;
        } catch (e) { toast(e.message, 'error'); btn.disabled = false; btn.textContent = o; }
    }
    const metaRow = (l, v) => el('div', { class: 'inv-metarow' }, el('span', { class: 'inv-muted' }, l), el('span', {}, v));
    const totRow = (l, v, strong) => el('div', { class: 'inv-totrow' + (strong ? ' strong' : '') }, el('span', {}, l), el('span', { class: 'num' }, v));
    function leistungLabel(from, to) {
        const f = new Date(from).toLocaleDateString('de-DE');
        const t = to ? new Date(to).toLocaleDateString('de-DE') : f;
        return f === t ? f : (f + ' – ' + t);
    }
    function invoiceDocument(iv) {
        const s = iv.seller || {}, b = iv.buyer || {};
        const doc = el('div', { class: 'invoice-doc' });
        doc.append(el('div', { class: 'inv-head' },
            el('div', { class: 'inv-seller' },
                el('div', { class: 'inv-seller-name' }, esc(s.name || 'Aussteller')),
                s.address ? el('div', { class: 'inv-muted pre' }, esc(s.address)) : null,
                s.uid ? el('div', { class: 'inv-muted' }, 'UID: ' + esc(s.uid)) : null),
            el('div', { class: 'inv-title' }, el('div', { class: 'inv-h' }, 'RECHNUNG'), el('div', { class: 'inv-num' }, esc(iv.number)))));
        doc.append(el('div', { class: 'inv-parties' },
            el('div', {}, el('div', { class: 'inv-label' }, 'Rechnung an'),
                el('div', { class: 'inv-strong' }, esc(b.name || '')),
                b.address ? el('div', { class: 'inv-muted pre' }, esc(b.address)) : null),
            el('div', { class: 'inv-meta' },
                metaRow('Rechnungsdatum', new Date(iv.issued_on).toLocaleDateString('de-DE')),
                iv.due_on ? metaRow('Fällig bis', new Date(iv.due_on).toLocaleDateString('de-DE')) : null,
                // § 11 Abs 1 Z 4: Leistungszeitraum (a period, or a single date if from==to).
                iv.leistung_from ? metaRow('Leistungszeitraum', leistungLabel(iv.leistung_from, iv.leistung_to)) : null,
                metaRow('Rechnungsnr.', esc(iv.number)))));
        const tb = el('tbody', {});
        (iv.items || []).forEach((it) => tb.append(el('tr', {},
            el('td', {}, String(it.pos)), el('td', {}, esc(it.description)),
            el('td', { class: 'r' }, fmtQty(it.quantity)),
            el('td', { class: 'r' }, eur(it.unit_amount)),
            el('td', { class: 'r' }, eur(it.line_total)))));
        doc.append(el('table', { class: 'inv-table' },
            el('thead', {}, el('tr', {}, el('th', {}, 'Pos'), el('th', {}, 'Beschreibung'),
                el('th', { class: 'r' }, 'Menge'), el('th', { class: 'r' }, iv.kleinunternehmer ? 'Betrag' : 'Netto'), el('th', { class: 'r' }, 'Summe'))),
            tb));
        const tot = el('div', { class: 'inv-totals' });
        if (iv.kleinunternehmer) {
            tot.append(totRow('Gesamt', eur(iv.total), true));
        } else {
            tot.append(totRow('Netto', eur(iv.subtotal)));
            tot.append(totRow('USt ' + fmtRate(iv.ust_rate) + ' %', eur(iv.tax_amount)));
            tot.append(totRow('Gesamt (brutto)', eur(iv.total), true));
        }
        doc.append(tot);
        if (iv.kleinunternehmer) doc.append(el('div', { class: 'inv-note' }, 'Umsatzsteuerbefreit gemäß § 6 Abs. 1 Z 27 UStG (Kleinunternehmer).'));
        if (s.iban) doc.append(el('div', { class: 'inv-pay' }, 'Zahlbar auf: ' + esc(s.iban) + (s.bic ? ' · BIC ' + esc(s.bic) : '')));
        if (iv.note) doc.append(el('div', { class: 'inv-note' }, esc(iv.note)));
        if (s.footer) doc.append(el('div', { class: 'inv-footer' }, esc(s.footer)));
        return doc;
    }
    routes.invoices = async (page, id) => {
        if (!id) { page.innerHTML = ''; page.append(emptyState('receipt', 'Keine Rechnung gewählt.')); return; }
        const iv = await api.get('/invoices/' + id);
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head no-print' },
            el('button', { class: 'back-btn', onclick: () => history.back(), 'aria-label': 'Zurück' }, '‹'),
            el('h2', { style: 'margin:0;flex:1' }, 'Rechnung ' + esc(iv.number), ' ', invStatusBadge(iv)),
            (canBill() && (iv.status === 'offen' || iv.status === 'teilbezahlt')) ? el('button', { class: 'btn btn-primary btn-sm', onclick: () => payInvoiceFor(iv) }, 'Bezahlen') : null,
            (canBill() && !iv.canceled && iv.status !== 'storno') ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => stornoInvoice(iv) }, 'Storno') : null,
            // Payment reminder by e-mail — only when the invoice is still open and
            // SMTP is configured (capability flag), so the button never dead-ends.
            (canBill() && (iv.status === 'offen' || iv.status === 'teilbezahlt') && state.capabilities.mail)
                ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => remindInvoice(iv) }, icon('mail', 15), ' Erinnerung') : null,
            // Server-rendered A4 PDF (authoritative deliverable). A plain GET anchor
            // carries the session cookie and opens the document inline.
            el('a', { class: 'btn btn-ghost btn-sm', href: '/api/invoices/' + id + '/pdf', target: '_blank', rel: 'noopener' }, icon('receipt', 15), ' PDF'),
            el('button', { class: 'btn btn-ghost btn-sm', onclick: () => window.print() }, 'Drucken')));
        if (iv.status === 'teilbezahlt' || iv.status === 'bezahlt') {
            page.append(el('div', { class: 'card-meta no-print', style: 'margin:-.3rem 0 .4rem' }, 'Bezahlt ' + eur(iv.paid_amount) + ' von ' + eur(iv.total) + (iv.open_amount > 0.005 ? ' · offen ' + eur(iv.open_amount) : '')));
        }
        page.append(invoiceDocument(iv));
        // SEPA pay-QR (Girocode) for open invoices whose seller has an IBAN. Hidden
        // in print — the PDF carries its own QR.
        if ((iv.status === 'offen' || iv.status === 'teilbezahlt') && iv.seller && iv.seller.iban) {
            page.append(el('div', { class: 'card no-print', style: 'text-align:center' },
                el('h3', { style: 'margin-top:0' }, 'Bezahlen (SEPA-QR)'),
                el('img', { src: '/api/invoices/' + iv.id + '/pay-qr', alt: 'SEPA-Zahlungs-QR', width: 200, height: 200, style: 'max-width:200px;height:auto' }),
                el('p', { class: 'muted', style: 'font-size:.82rem;margin-bottom:0' }, 'Mit der Banking-App scannen — Betrag und Zahlungsreferenz sind vorausgefüllt.')));
        }
    };

    // ---------- Rechnungs-Einstellungen (admin) ----------
    routes.billing = async (page) => {
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('shield', 'Nur für Administratoren.')); return; }
        const s = (await api.get('/billing/settings')) || {};
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', onclick: () => navigate('dashboard'), 'aria-label': 'Zurück' }, '‹'),
            el('h2', { style: 'margin:0' }, 'Rechnungs-Einstellungen')));

        const field = (label, input, help) => el('div', { class: 'bill-field' },
            el('label', {}, label), input, help ? el('div', { class: 'card-meta' }, help) : null);
        const inp = (name, val, attrs = {}) => el('input', Object.assign({ id: 'bs_' + name, value: val ?? '' }, attrs));

        const seller = el('div', { class: 'card' }, el('h3', {}, 'Aussteller'),
            field('Name / Firma', inp('seller_name', s.seller_name)),
            field('Adresse', el('textarea', { id: 'bs_seller_address', rows: '2' }, s.seller_address || '')),
            field('UID-Nummer (ATU…)', inp('seller_uid', s.seller_uid), 'Pflicht bei USt-Ausweis.'));

        const klCheck = el('input', { type: 'checkbox', id: 'bs_klein' });
        klCheck.checked = s.kleinunternehmer !== false;
        const rateSel = el('select', { id: 'bs_rate' },
            ...[20, 13, 10].map((r) => { const o = el('option', { value: String(r) }, r + ' %'); if (Number(s.ust_rate) === r) o.selected = true; return o; }));
        const rateWrap = field('USt-Satz', rateSel, 'Österreich: 20 % (Regelsatz, u. a. Einstellplatz), 13 %, 10 %.');
        const applyKl = () => { rateWrap.style.display = klCheck.checked ? 'none' : ''; };
        klCheck.addEventListener('change', applyKl);
        const tax = el('div', { class: 'card' }, el('h3', {}, 'Steuer'),
            el('label', { class: 'switch' }, klCheck, el('span', { class: 'track' }), el('span', {}, 'Kleinunternehmer (§ 6 Abs 1 Z 27 UStG – keine USt)')),
            rateWrap);
        applyKl();

        const numbering = el('div', { class: 'card' }, el('h3', {}, 'Nummerierung & Zahlung'),
            field('Rechnungsnr.-Präfix', inp('prefix', s.invoice_prefix, { placeholder: '2026-' })),
            field('Nächste Nummer', inp('next_no', s.next_invoice_no ?? 1, { type: 'number', min: '1' }), 'Kann nur vorwärts gesetzt werden.'),
            field('Stellen (Nullen)', inp('pad', s.number_pad ?? 4, { type: 'number', min: '1', max: '10' })),
            field('Zahlungsziel (Tage)', inp('terms', s.payment_terms_days ?? 14, { type: 'number', min: '0' })),
            field('IBAN', inp('iban', s.iban)),
            field('BIC', inp('bic', s.bic)),
            field('Fußnote', el('textarea', { id: 'bs_footer', rows: '2' }, s.footer_note || '')));

        const saveBtn = el('button', { class: 'btn btn-primary' }, 'Einstellungen speichern');
        saveBtn.addEventListener('click', async () => {
            const val = (id) => (document.getElementById(id) || {}).value;
            const payload = {
                seller_name: val('bs_seller_name'), seller_address: val('bs_seller_address'), seller_uid: val('bs_seller_uid'),
                kleinunternehmer: klCheck.checked, ust_rate: Number(rateSel.value),
                invoice_prefix: val('bs_prefix'), next_invoice_no: Number(val('bs_next_no')) || 1,
                number_pad: Number(val('bs_pad')) || 4, payment_terms_days: Number(val('bs_terms')) || 0,
                iban: val('bs_iban'), bic: val('bs_bic'), footer_note: val('bs_footer'),
            };
            saveBtn.disabled = true;
            try { await api.post('/billing/settings', payload); toast('Gespeichert', 'success'); }
            catch (e) { toast(e.message, 'error'); }
            saveBtn.disabled = false;
        });
        page.append(seller, tax, numbering, el('div', { style: 'margin-top:1rem' }, saveBtn));
    };

    // ================= VEHICLES =================
    routes.vehicles = async (page) => {
        await refreshLookups();
        const vehicles = await api.get('/vehicles');
        mountList(page, {
            title: 'Gefährte', emptyIcon: 'car', emptyText: 'Keine Gefährte in dieser Ansicht.',
            onAdd: canManage() ? () => vehicleForm() : null,
            items: vehicles,
            searchText: (v) => norm([v.label, v.license_plate, v.category_name, v.person_name].join(' ')),
            sorts: [
                { label: 'Neueste zuerst', cmp: (a, b) => new Date(b.start_date) - new Date(a.start_date) },
                { label: 'Bezeichnung', cmp: (a, b) => norm(a.label || a.license_plate).localeCompare(norm(b.label || b.license_plate)) },
                { label: 'Kosten absteigend', cmp: (a, b) => b.accrued_cost - a.accrued_cost },
            ],
            controls: (refresh, cs) => {
                cs.status = ''; cs.person = ''; cs.showArchived = false;
                const stSel = el('select', {}, el('option', { value: '' }, 'Alle Status'),
                    ...['stored', 'reserved', 'collected', 'cancelled'].map((s) => el('option', { value: s }, STATUS_LABEL[s])));
                stSel.addEventListener('change', () => { cs.status = stSel.value; refresh(); });
                const peSel = el('select', {}, el('option', { value: '' }, 'Alle Personen'),
                    ...state.persons.map((p) => el('option', { value: p.id }, personName(p))));
                peSel.addEventListener('change', () => { cs.person = peSel.value; refresh(); });
                const arChk = el('input', { type: 'checkbox' });
                arChk.addEventListener('change', () => { cs.showArchived = arChk.checked; refresh(); });
                const arLabel = el('label', { class: 'toggle-inline' }, arChk, el('span', {}, 'Archiv'));
                return [stSel, peSel, arLabel];
            },
            // Hide archived (closed) vehicles unless the Archiv toggle is on.
            extraFilter: (v, cs) => (cs.showArchived || !v.archived) &&
                (!cs.status || v.status === cs.status) && (!cs.person || String(v.person_id) === cs.person),
            render: (v) => vehicleCard(v, { linkable: true }),
        });
    };

    // coverage summarises a vehicle's flat-rate state for display:
    //  - covered: ever covered by a Pauschale (drives which amount is "owed")
    //  - active:  a Pauschale covers it today (drives the "in Pauschale" label)
    //  - owed:    amount owed per vehicle (uncovered remainder, or full accrued)
    //  - settled: covered and nothing owed per vehicle -> billed via the Pauschale
    function coverage(v) {
        const covered = !!v.flat_rate_covered;
        const active = !!v.flat_rate_active;
        const owed = covered ? (Number(v.uncovered_cost) || 0) : (Number(v.accrued_cost) || 0);
        return { covered, active, owed, settled: covered && owed <= 0.005 };
    }

    // Custom planner-icon library: upload PNG/JPEG top-view icons with a name/tag, list
    // and delete them. Uploaded icons become selectable per vehicle (planner_symbol =
    // 'custom:<id>'). Returns after the dialog closes so callers can refresh their selects.
    async function plannerIconsDialog() {
        const dlg = el('dialog', { class: 'iconmodal' });
        const body = el('div', {});
        let editId = null, editName = ''; // when set, the top form renames / replaces that icon
        const render = async () => {
            body.innerHTML = '';
            body.append(el('h3', {}, 'Planer-Icons verwalten'));
            body.append(el('p', { class: 'muted', style: 'font-size:.8rem;margin:.1rem 0 .6rem' }, editId ? 'Bearbeiten: Name ändern und/oder neue Datei wählen — die ID (und damit alle Zuordnungen) bleibt erhalten.' : 'Eigene Draufsicht-Icons (PNG/JPEG) hochladen und benennen. Danach pro Gefährt unter „Planer-Symbol" wählbar.'));
            const nameIn = el('input', { class: 'gp-stepin', placeholder: 'Name / Tag, z. B. Pferdeanhänger', value: editId ? editName : '' });
            const fileIn = el('input', { type: 'file', accept: 'image/png,image/jpeg' });
            const submit = el('button', {
                class: 'btn btn-primary btn-sm', onclick: async () => {
                    const nm = nameIn.value.trim();
                    if (editId) {
                        if (!nm && !fileIn.files[0]) { toast('Name oder Datei ändern', 'error'); return; }
                        const fd = new FormData(); if (nm) fd.append('name', nm); if (fileIn.files[0]) fd.append('file', fileIn.files[0]);
                        try { await api.upload('/planner-icons/' + editId, fd, 'PUT'); toast('Icon aktualisiert', 'success'); editId = null; await render(); }
                        catch (err) { toast(err.message || 'Speichern fehlgeschlagen', 'error'); }
                    } else {
                        if (!nm || !fileIn.files[0]) { toast('Name und Datei nötig', 'error'); return; }
                        const fd = new FormData(); fd.append('name', nm); fd.append('file', fileIn.files[0]);
                        try { await api.upload('/planner-icons', fd); toast('Icon hochgeladen', 'success'); await render(); }
                        catch (err) { toast(err.message || 'Upload fehlgeschlagen', 'error'); }
                    }
                }
            }, editId ? 'Speichern' : 'Hochladen');
            const row = el('div', { class: 'iconup' }, nameIn, fileIn, submit);
            if (editId) row.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => { editId = null; render(); } }, 'Abbrechen'));
            body.append(row);
            let icons = []; try { icons = await api.get('/planner-icons'); } catch (e) { /* offline */ }
            const grid = el('div', { class: 'icongrid' });
            if (!icons.length) grid.append(el('div', { class: 'muted', style: 'font-size:.82rem' }, 'Noch keine eigenen Icons.'));
            icons.forEach((ic) => grid.append(el('div', { class: 'iconcell' + (editId === ic.id ? ' editing' : '') },
                el('img', { src: '/api/planner-icons/' + ic.id + '?t=' + (ic.byte_size || 0), alt: ic.name }),
                el('span', { title: ic.name }, ic.name),
                el('button', { class: 'btn btn-ghost btn-sm iconedit', title: 'Umbenennen / Bild ersetzen', onclick: () => { editId = ic.id; editName = ic.name; render(); } }, '✎'),
                el('button', { class: 'btn btn-ghost btn-sm icondel', title: 'Löschen', onclick: async () => { if (!await confirmDialog('Icon löschen?', `„${ic.name}" wird entfernt. Fahrzeuge, die es nutzen, fallen aufs Kategorie-Symbol zurück.`, 'Löschen')) return; try { await api.del('/planner-icons/' + ic.id); if (editId === ic.id) editId = null; await render(); } catch (e) { toast('Löschen fehlgeschlagen', 'error'); } } }, '✕'))));
            body.append(grid);
            // Built-in category icons (read-only): always available per vehicle under
            // „Planer-Symbol", not stored in the DB and therefore not editable/deletable.
            body.append(el('h4', { class: 'iconh4' }, 'Eingebaute Icons'));
            body.append(el('p', { class: 'muted', style: 'font-size:.76rem;margin:0 0 .5rem' }, 'Immer verfügbar, pro Gefährt unter „Planer-Symbol" wählbar. Nicht löschbar.'));
            const bgrid = el('div', { class: 'icongrid' });
            Object.entries(CATICON).forEach(([cat, slug]) => bgrid.append(el('div', { class: 'iconcell builtin' },
                el('img', { src: CATICON_BASE + slug + '.png', alt: cat }),
                el('span', { title: cat }, cat))));
            body.append(bgrid);
            body.append(el('button', { class: 'btn btn-ghost btn-sm', style: 'margin-top:.7rem', onclick: () => dlg.close() }, 'Schließen'));
        };
        dlg.append(body); document.body.append(dlg);
        dlg.addEventListener('close', () => dlg.remove());
        await render(); dlg.showModal();
        await new Promise((res) => dlg.addEventListener('close', res, { once: true }));
    }

    // Standort-Chip: quick link Gefährt → its Stellplatz in the planner (or a muted
    // "not placed"). Click focuses the spot on the plan (pendingSpotFocus + navigate).
    function vehLocationChip(v, { compact = false } = {}) {
        if (v.spot_id && v.hall_id) {
            const go = (e) => { e.stopPropagation(); pendingSpotFocus = v.spot_id; navigate('hall/' + v.hall_id); };
            const title = v.hall_name ? 'Im Plan zeigen: Halle „' + v.hall_name + '"' : 'Im Plan zeigen';
            if (compact) return el('button', { class: 'veh-loc veh-loc-ic', title, 'aria-label': title, onclick: go }, icon('pin', 20));
            return el('button', { class: 'veh-loc', title: 'Im Plan anzeigen', onclick: go },
                el('span', {}, '📍 ' + (v.hall_name ? 'Halle „' + v.hall_name + '"' : 'im Plan')),
                el('span', { class: 'veh-loc-go' }, 'Im Plan zeigen →'));
        }
        if (compact) return el('span', { class: 'veh-loc veh-loc-ic empty', title: 'Nicht platziert', 'aria-label': 'Nicht platziert' }, icon('pin', 20));
        return el('span', { class: 'veh-loc empty' }, '○ Nicht platziert');
    }
    function vehicleCard(v, { linkable = true, chargeInfo = null } = {}) {
        const title = vehicleTitle(v);
        const rateUnit = v.billing_period === 'yearly' ? '/Jahr' : '/Monat';
        const c = coverage(v);
        // Show "über Pauschale" only when nothing is owed per vehicle. Once a
        // Pauschale ends, per-vehicle billing resumes, so the rate + the owed
        // remainder are shown again (an expired Pauschale is no longer "aktiv").
        const period = `seit ${fmtDate(v.start_date)}` + (v.end_date ? ` bis ${fmtDate(v.end_date)}` : '');
        const rateLine = c.settled ? ('über Pauschale · ' + period) : (`${eur(v.effective_rate)}${rateUnit} · ` + period);
        let accruedLine;
        if (c.settled) accruedLine = el('div', { class: 'card-meta', style: 'margin-top:.3rem' }, 'Abrechnung über Pauschale');
        else accruedLine = el('div', { class: 'card-meta', style: 'color:var(--text);font-weight:600;margin-top:.3rem' },
            (c.covered ? 'Aufgelaufen (Rest): ' : 'Aufgelaufen: ') + eur(c.owed));
        const main = el('div', { style: 'flex:1;' + (linkable ? 'cursor:pointer' : ''), onclick: linkable ? () => navigate('vehicles/' + v.id) : null },
            el('h3', {}, esc(title)),
            el('div', { class: 'card-meta' }, el('span', { class: 'badge badge-cat' }, esc(v.category_name)), ' ', esc(v.person_name),
                v.photo_count ? el('span', { class: 'muted' }, '  ', icon('camera', 13), ' ' + v.photo_count) : null),
            el('div', { class: 'card-meta' }, rateLine),
            accruedLine);
        // Bound extra costs attributed to this Gefährt (also shown when nested under
        // a Pauschale), so the total picture is visible without opening the detail.
        if (chargeInfo && chargeInfo.sum > 0.005) {
            main.append(el('div', { class: 'card-meta', style: 'margin-top:.2rem;color:var(--accent-text);font-weight:600' },
                'Zusatzkosten: ' + eur(chargeInfo.sum) + (chargeInfo.count > 1 ? ' · ' + chargeInfo.count + ' Pos.' : '')));
        }
        const actions = el('div', { class: 'card-actions stack' },
            // Archived vehicles are read-only, so no inline edit; delete stays for
            // genuine mistakes.
            el('div', { class: 'card-actbtns' },
                canManage() && !v.archived && el('button', { class: 'btn btn-ghost btn-sm', title: title + ' bearbeiten', 'aria-label': title + ' bearbeiten', onclick: () => vehicleForm(v) }, icon('edit')),
                canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: title + ' löschen', 'aria-label': title + ' löschen', onclick: (e) => delVehicle(v, e.currentTarget.closest('.card')) }, icon('trash'))),
            // Standort as a compact pin icon under the edit/delete buttons — keeps the card
            // no taller than necessary; the hall name is in its tooltip/aria-label.
            vehLocationChip(v, { compact: true }));
        return el('div', { class: 'card' + (v.archived ? ' is-archived' : '') },
            el('div', { class: 'card-row' }, main, actions),
            vehicleControls(v));
    }

    // Slider-based quick controls: status + payment, shown on card and detail.
    // When the vehicle is covered by a flat-rate agreement an "in Pauschale" hint
    // is shown; the payment slider still marks the uncovered portion as paid.
    function vehicleControls(v) {
        const wrap = el('div', { class: 'controls-row' });
        const c = coverage(v);
        const pauschaleBadge = (label, title) => el('span', { class: 'badge badge-cat', title }, label);
        // An invoiced vehicle settles via its invoice, not the per-vehicle slider:
        // "fakturiert" while a covering invoice is still open, "bezahlt · Rechnung"
        // once all covering invoices are paid.
        const invoiceBadge = () => v.invoice_open
            ? el('span', { class: 'badge badge-collected', title: 'Fakturiert – über die Rechnung begleichen' }, 'fakturiert')
            : el('span', { class: 'badge badge-active', title: 'Über eine bezahlte Rechnung beglichen' }, 'bezahlt · Rechnung');
        // Archived (closed) vehicles are read-only: show the settled state as
        // badges and offer a one-tap reactivate instead of the live sliders.
        if (v.archived) {
            wrap.append(statusBadge(v.status));
            // Nothing owed per vehicle => billed via the Pauschale; otherwise show
            // the per-vehicle paid state.
            if (c.settled) {
                wrap.append(pauschaleBadge('über Pauschale abgerechnet', 'Zahlung erfolgt über die Pauschale'));
            } else if (v.invoiced) {
                wrap.append(invoiceBadge());
            } else {
                wrap.append(el('span', { class: 'badge ' + (v.paid ? 'badge-active' : 'badge-ended') }, v.paid ? 'bezahlt' : 'offen'));
                if (c.active) wrap.append(pauschaleBadge('in Pauschale', 'Teilweise über eine Pauschale abgerechnet'));
            }
            if (canManage()) {
                // "Erneut einstellen" is safe on archived vehicles: it creates a new
                // vehicle and never mutates this (read-only) record.
                wrap.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => duplicateVehicle(v) }, icon('redo'), ' Erneut einstellen'));
                wrap.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => reactivateVehicle(v) }, icon('undo'), ' Reaktivieren'));
            }
            return wrap;
        }
        // Status stays editable even when covered — it is the physical lifecycle
        // (reserved/stored/collected), independent of billing.
        wrap.append(statusSlider(v));
        if (canBill()) {
            if (c.settled) {
                // Nothing owed per vehicle; payment belongs to the Pauschale.
                wrap.append(pauschaleBadge('über Pauschale abgerechnet', 'Zahlung erfolgt über die Pauschale'));
            } else if (v.invoiced) {
                // Settled via its invoice, not the slider — show the invoice state.
                wrap.append(invoiceBadge());
            } else {
                // A remainder is owed (e.g. the Pauschale has ended, or covers only
                // part of the period) — settle it with the per-vehicle slider.
                wrap.append(paidSlider(v));
                if (c.active) wrap.append(pauschaleBadge('in Pauschale', 'Teilweise über eine Pauschale abgerechnet – der Slider betrifft nur den Restbetrag'));
            }
        } else if (c.active || c.settled) {
            wrap.append(pauschaleBadge('in Pauschale', 'Über eine Pauschale abgerechnet'));
        }
        if (canManage() && v.status === 'collected') {
            wrap.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => duplicateVehicle(v) }, icon('redo'), ' Erneut einstellen'));
        }
        return wrap;
    }
    async function reactivateVehicle(v) {
        try { await api.post('/vehicles/' + v.id + '/reactivate'); toast('Reaktiviert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); render(); }
    }

    // ---- flat-rate agreements (Pauschale-Einträge) ----
    // Share of the accrued amount that is settled (full periods + fixed partials,
    // or the master paid flag). Returns the pieces the card needs: accrued, paid,
    // still-open, fraction, and whether it's fully done.
    function agreementPaid(a) {
        const accrued = Number(a.accrued) || 0;
        let paidAcc;
        if (a.paid) {
            paidAcc = accrued;
        } else {
            paidAcc = 0;
            const costs = a.period_costs || {};
            const counted = new Set();
            // Whole periods paid via the per-period toggle, then those settled through
            // a fully-paid Rechnung (invoice_paid_periods) — both count at full cost.
            for (const k of (a.paid_periods || [])) { paidAcc += Number(costs[k]) || 0; counted.add(k); }
            for (const k of (a.invoice_paid_periods || [])) {
                if (counted.has(k)) continue;
                paidAcc += Number(costs[k]) || 0; counted.add(k);
            }
            const fixed = a.paid_fixed || {};
            for (const k in fixed) {
                if (counted.has(k)) continue; // whole period already counted
                const c = Number(costs[k]) || 0;
                paidAcc += c > 0 ? Math.min(Number(fixed[k]) || 0, c) : (Number(fixed[k]) || 0);
            }
        }
        paidAcc = Math.min(paidAcc, accrued);
        const frac = accrued > 0.005 ? Math.max(0, Math.min(1, paidAcc / accrued)) : (a.paid ? 1 : 0);
        return {
            accrued, paidAcc, frac,
            open: Math.max(0, accrued - paidAcc),
            done: accrued > 0.005 ? paidAcc >= accrued - 0.005 : true,
        };
    }
    // Payment-progress bar for a Pauschale: a gradient fill + a % marker, and a
    // caption that leads with what's still open — or a calm "vollständig bezahlt"
    // once it's settled. Hidden until something has accrued.
    function agreementProgress(a) {
        const pd = agreementPaid(a);
        if (pd.accrued <= 0.005) return null;
        const pct = Math.round(pd.frac * 100);
        const fill = el('div', { class: 'bar-fill' });
        requestAnimationFrame(() => { fill.style.width = (pd.frac * 100) + '%'; });
        const cap = pd.done
            ? el('div', { class: 'ag-progress-cap is-done' }, '✓ vollständig bezahlt')
            : el('div', { class: 'ag-progress-cap' },
                el('span', { class: 'off' }, eur(pd.open) + ' offen'), ' · ' + pct + ' % bezahlt');
        return el('div', { class: 'ag-progress' },
            el('div', { class: 'ag-progress-row' },
                el('div', { class: 'bar-track ag-bar' }, fill),
                el('span', { class: 'ag-pct' + (pd.done ? ' is-done' : '') }, pct + ' %')),
            cap);
    }
    // One-tap settle: mark the whole Pauschale paid, with an Undo. Only offered on
    // a fully-open Pauschale, so Undo (paid=false) can't wipe partial per-period
    // payments (the master flag clears them server-side).
    async function agreementMarkAllPaid(a) {
        try {
            await api.post('/agreements/' + a.id + '/paid', { paid: true });
            // Re-render first, then raise the Undo toast last so the re-render
            // can't steal attention from it; keep it up long enough to notice.
            await render();
            toastAction('Als bezahlt markiert', 'Rückgängig', async () => {
                try { await api.post('/agreements/' + a.id + '/paid', { paid: false }); render(); }
                catch (e) { toast(e.message, 'error'); }
            }, 8000);
        } catch (e) { toast(e.message, 'error'); render(); }
    }
    function agreementRow(personId, a, vehicles, chargeByVeh = {}) {
        const unit = a.period === 'yearly' ? '/Jahr' : '/Monat';
        // Payment state is carried by the progress bar + caption (and the amount
        // turns amber only while something is open), so no separate status badge
        // here — only the coverage badge (+ a "beendet" tag for expired ones).
        const pd = agreementPaid(a);
        const isEnded = a.end_date && a.end_date.slice(0, 10) <= today();
        const nCov = (a.vehicle_ids && a.vehicle_ids.length) ? a.vehicle_ids.length + ' Gefährte' : 'alle Gefährte';
        // Terms only: rate + span (+ note). The covered vehicles live in the
        // "N Gefährte" badge and the "Gefährte" section, so they're not repeated
        // here. Open-ended reads "seit <start>" rather than "<start> – offen".
        const config = eur(a.amount) + unit + ' · '
            + (a.end_date ? fmtDate(a.start_date) + ' – ' + fmtDate(a.end_date) : 'seit ' + fmtDate(a.start_date))
            + (a.note ? ' · ' + esc(a.note) : '');
        // A one-tap "als bezahlt" is only safe (Undo-lossless) when nothing has
        // been paid yet, so offer it just for a fully-open Pauschale.
        const canQuickSettle = canBill() && pd.accrued > 0.005 && pd.paidAcc <= 0.005;
        const row = el('div', { class: 'card', style: 'margin-top:.5rem' });
        row.append(el('div', { class: 'card-row' },
            el('div', { class: 'ag-body' },
                el('div', { class: 'ag-status-row' },
                    el('span', { class: 'badge badge-cat' }, nCov),
                    isEnded ? el('span', { class: 'badge ag-ended-tag' }, 'beendet ' + fmtDate(a.end_date)) : null),
                el('div', { class: 'ag-accrued' + (pd.done ? '' : ' is-open') },
                    eur(a.accrued), el('span', { class: 'unit' }, ' aufgelaufen')),
                agreementProgress(a),
                el('div', { class: 'card-meta' }, config)),
            canBill() ? el('div', { class: 'card-actions' },
                canQuickSettle ? el('button', { class: 'btn btn-ghost btn-sm ag-check', title: 'Ganze Pauschale als bezahlt markieren', 'aria-label': 'Pauschale als bezahlt markieren', onclick: () => agreementMarkAllPaid(a) }, icon('check')) : null,
                el('button', { class: 'btn btn-ghost btn-sm', title: 'Pauschale ab ' + fmtDate(a.start_date) + ' bearbeiten', 'aria-label': 'Pauschale ab ' + fmtDate(a.start_date) + ' bearbeiten', onclick: () => agreementForm(personId, vehicles, a) }, icon('edit')),
                el('button', { class: 'btn btn-ghost btn-sm', title: 'Pauschale ab ' + fmtDate(a.start_date) + ' löschen', 'aria-label': 'Pauschale ab ' + fmtDate(a.start_date) + ' löschen', onclick: () => delAgreement(a) }, icon('trash'))) : null));
        // Readers already see the status via the ag-status-row chip above; only
        // billers get the interactive payment controls.
        if (canBill()) row.append(agreementPayments(a));
        // Subordinate vehicles nested under the Pauschale (they no longer appear in
        // the separate Gefährte list). Empty vehicle_ids = covers all vehicles.
        const sub = (a.vehicle_ids && a.vehicle_ids.length)
            ? vehicles.filter((v) => a.vehicle_ids.includes(v.id))
            : vehicles;
        if (sub.length) {
            const det = persistDetails('ag-veh-' + a.id, 'archive-section', 'Gefährte (' + sub.length + ')', false);
            sub.forEach((v) => det.append(vehicleCard(v, { linkable: true, chargeInfo: chargeByVeh[v.id] })));
            row.append(det);
        }
        return row;
    }
    // Payment controls for an agreement: a master "Alle" slider (bezahlt = all
    // paid, offen = none) plus a collapsible per-period list (per year for yearly
    // agreements, per month for monthly ones) so single periods can be settled.
    function agreementPayments(a) {
        // Master "Alle Zeiträume" slider (bezahlt = all paid, offen = none).
        const master = el('div', { class: 'controls-row' },
            el('span', { class: 'muted', style: 'font-size:.85rem' }, 'Alle Zeiträume'), agreementPaidSlider(a));
        const periods = agreementPeriods(a);
        // Nothing has accrued yet: only the master toggle is relevant, show it bare.
        if (!periods.length) return master;
        // Key of the period that is currently running (still accruing), so it can
        // be marked "läuft" — here "bezahlt" means the whole period is prepaid.
        const curKey = a.period === 'yearly' ? today().slice(0, 4) : today().slice(0, 7);
        // Fold both the master toggle and the per-period list into one collapsed
        // section, so the card at rest shows status (badge/bar) but no controls.
        const det = persistDetails('ag-pay-' + a.id, 'period-payments', 'Zahlungen verwalten (' + periods.length + ')', false);
        det.append(master);
        const box = el('div', { class: 'period-list' });
        for (const p of periods) {
            const amt = a.period_costs ? a.period_costs[p.key] : null;
            const running = p.key === curKey;
            const fx = periodFixed(a, p.key);
            box.append(el('div', { class: 'period-row' },
                el('span', { class: 'period-label' }, p.label,
                    amt != null ? el('span', { class: 'period-amt' }, ' · ' + eur(amt)) : null,
                    running ? el('span', { class: 'period-amt', title: '„bezahlt": ganze Periode im Voraus – oder Teilbetrag wählen' }, ' · läuft') : null,
                    fx != null ? el('span', { class: 'period-amt', title: 'Teilbetrag bezahlt – Rest offen' }, ' · Teil ' + eur(fx)) : null),
                agreementPeriodSlider(a, p.key, running, amt)));
        }
        det.append(box);
        return det;
    }
    // agreementPeriods lists the elapsed sub-periods, newest first. The keys come
    // straight from the backend (period_costs, keyed "YYYY" / "YYYY-MM") — the
    // single source of truth for which periods exist — so the client can never
    // render a payable period the server doesn't bill (e.g. the excluded month
    // when an agreement ends exactly on a month's first day).
    function agreementPeriods(a) {
        const keys = Object.keys(a.period_costs || {});
        keys.sort().reverse(); // zero-padded keys: lexicographic = chronological
        return keys.map((k) => ({
            key: k,
            label: a.period === 'yearly' ? k : MONTH_NAMES[Number(k.slice(5)) - 1] + ' ' + k.slice(0, 4),
        }));
    }
    const periodFixed = (a, key) => (a.paid_fixed && a.paid_fixed[key] != null) ? a.paid_fixed[key] : null;
    const periodPaid = (a, key) => !!a.paid || (a.paid_periods || []).includes(key) || periodFixed(a, key) != null;
    // For a still-running period, ask whether "bezahlt" means the whole period was
    // prepaid or only a fixed Teilbetrag. Returns { amount } (null = whole) or null.
    async function periodPayDialog(label, defAmt, current) {
        const mode = current ? current.mode : 'full';
        const amtVal = current && current.mode === 'partial' ? current.amount : (defAmt != null ? defAmt : '');
        const data = await formModal({
            title: 'Zahlung ' + label,
            submitLabel: 'Speichern',
            fields: [
                { name: 'mode', label: 'Art der Zahlung', type: 'select', value: mode,
                    options: [{ value: 'full', label: 'Ganze Pauschale (im Voraus)' }, { value: 'partial', label: 'Teilbetrag' }] },
                { name: 'amount', label: 'Teilbetrag (€)', type: 'number', step: '0.01', min: 0, value: amtVal,
                    help: 'Nur bei „Teilbetrag": bereits fälliger Betrag. Der Rest des Zeitraums bleibt offen.' },
            ],
        });
        if (!data) return null;
        if (data.mode === 'partial') return { amount: data.amount === '' ? 0 : Number(data.amount) };
        return { amount: null };
    }
    function agreementPeriodSlider(a, key, running, defAmt) {
        const paid = periodPaid(a, key);
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus ' + key });
        const post = async (val, amount) => {
            try {
                const body = { period_key: key, paid: val };
                if (val && amount != null) body.amount = amount;
                await api.post('/agreements/' + a.id + '/period-paid', body);
                toast(val ? 'bezahlt' : 'offen', 'success'); render();
            } catch (err) { toast(err.message, 'error'); render(); }
        };
        const onPaid = async (e) => {
            markActive(e.currentTarget);
            const fixed = periodFixed(a, key);
            // Offer / adjust whole-vs-Teilbetrag on a running period, or on any
            // period that already carries a fixed Teilbetrag (so it can be edited).
            if (running || fixed != null) {
                const current = fixed != null ? { mode: 'partial', amount: fixed } : (paid ? { mode: 'full' } : null);
                const choice = await periodPayDialog(key, defAmt, current);
                if (!choice) { render(); return; } // cancelled -> reset optimistic state
                // A partial of 0/blank is not a payment — revert instead of sending
                // an empty Teilbetrag the server rejects (mirrors the recurring slider).
                if (choice.amount != null && !(choice.amount > 0)) { render(); return; }
                await post(true, choice.amount);
                return;
            }
            await post(true, null);
        };
        seg.append(el('button', { class: !paid ? 'active open' : '', type: 'button', role: 'radio', 'aria-checked': String(!paid), onclick: (e) => { markActive(e.currentTarget); post(false, null); } }, 'offen'));
        seg.append(el('button', { class: paid ? 'active done' : '', type: 'button', role: 'radio', 'aria-checked': String(paid), onclick: onPaid }, 'bezahlt'));
        return segThumb(seg);
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
        return segThumb(seg);
    }

    // ---- Recurring extra costs (Wiederkehrende Nebenkosten) ----
    // A recurring charge shares the Pauschale's period/paid shape, so it reuses the
    // generic period helpers (agreementPeriods / periodFixed / periodPaid / dialog).
    function recurringRow(personId, rc) {
        const unit = rc.period === 'yearly' ? '/Jahr' : '/Monat';
        // A bound charge is settled via its Gefährt/Pauschale (read-only here); a
        // free one keeps its own per-period payment controls.
        const bound = rc.vehicle_id != null;
        const partial = !rc.paid && (((rc.paid_periods && rc.paid_periods.length)) || (rc.paid_fixed && Object.keys(rc.paid_fixed).length));
        let stLabel, stCls;
        if (bound) {
            stLabel = (rc.settled ? 'bezahlt' : 'offen') + ' · über ' + esc(rc.vehicle_label || 'Gefährt');
            stCls = rc.settled ? 'badge-active' : 'badge-ended';
        } else {
            stLabel = rc.paid ? 'Bezahlt' : partial ? 'Teilweise bezahlt' : 'Offen';
            stCls = rc.paid ? 'badge-active' : partial ? 'badge-cat' : 'badge-ended';
        }
        const open = bound ? !rc.settled : !rc.paid;
        const config = eur(rc.amount) + unit + ' · ' + fmtDate(rc.start_date) + (rc.end_date ? ' – ' + fmtDate(rc.end_date) : ' – offen');
        const row = el('div', { class: 'card', style: 'margin-top:.5rem' });
        row.append(el('div', { class: 'card-row' },
            el('div', { style: 'flex:1' },
                el('div', { class: 'ag-status-row' }, el('span', { class: 'badge ' + stCls }, stLabel)),
                el('div', {}, el('strong', {}, esc(rc.description))),
                el('div', { class: 'ag-accrued' + (open ? ' is-open' : '') }, eur(rc.accrued), el('span', { class: 'unit' }, ' aufgelaufen')),
                el('div', { class: 'card-meta' }, config)),
            canBill() ? el('div', { class: 'card-actions' },
                el('button', { class: 'btn btn-ghost btn-sm', title: rc.description + ' bearbeiten', 'aria-label': rc.description + ' bearbeiten', onclick: () => recurringForm(personId, rc) }, icon('edit')),
                el('button', { class: 'btn btn-ghost btn-sm', title: rc.description + ' löschen', 'aria-label': rc.description + ' löschen', onclick: (e) => delRecurring(rc, e.currentTarget.closest('.card')) }, icon('trash'))) : null));
        // Bound charges follow the Gefährt/Pauschale — no own payment sliders.
        if (canBill() && !bound) row.append(recurringPayments(rc));
        return row;
    }
    function recurringPayments(rc) {
        const wrap = el('div', {});
        wrap.append(el('div', { class: 'controls-row' },
            el('span', { class: 'muted', style: 'font-size:.85rem' }, 'Alle Zeiträume'), recurringPaidSlider(rc)));
        const periods = agreementPeriods(rc);
        const curKey = rc.period === 'yearly' ? today().slice(0, 4) : today().slice(0, 7);
        if (periods.length) {
            const det = persistDetails('rec-pay-' + rc.id, 'period-payments', 'Zahlung je Zeitraum (' + periods.length + ')', false);
            const box = el('div', { class: 'period-list' });
            for (const p of periods) {
                const amt = rc.period_costs ? rc.period_costs[p.key] : null;
                const running = p.key === curKey;
                const fx = periodFixed(rc, p.key);
                box.append(el('div', { class: 'period-row' },
                    el('span', { class: 'period-label' }, p.label,
                        amt != null ? el('span', { class: 'period-amt' }, ' · ' + eur(amt)) : null,
                        running ? el('span', { class: 'period-amt', title: '„bezahlt": ganze Periode im Voraus – oder Teilbetrag wählen' }, ' · läuft') : null,
                        fx != null ? el('span', { class: 'period-amt', title: 'Teilbetrag bezahlt – Rest offen' }, ' · Teil ' + eur(fx)) : null),
                    recurringPeriodSlider(rc, p.key, running, amt)));
            }
            det.append(box);
            wrap.append(det);
        }
        return wrap;
    }
    function recurringPaidSlider(rc) {
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus wiederkehrende Kosten' });
        const setPaid = async (val, e) => {
            markActive(e.currentTarget);
            try { await api.post('/recurring/' + rc.id + '/paid', { paid: val }); toast(val ? 'bezahlt' : 'offen', 'success'); render(); }
            catch (err) { toast(err.message, 'error'); render(); }
        };
        seg.append(el('button', { class: (!rc.paid ? 'active open' : ''), type: 'button', role: 'radio', 'aria-checked': String(!rc.paid), onclick: (e) => setPaid(false, e) }, 'offen'));
        seg.append(el('button', { class: (rc.paid ? 'active done' : ''), type: 'button', role: 'radio', 'aria-checked': String(rc.paid), onclick: (e) => setPaid(true, e) }, 'bezahlt'));
        return segThumb(seg);
    }
    function recurringPeriodSlider(rc, key, running, defAmt) {
        const paid = periodPaid(rc, key);
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus ' + key });
        const post = async (val, amount) => {
            try {
                const body = { period_key: key, paid: val };
                if (val && amount != null) body.amount = amount;
                await api.post('/recurring/' + rc.id + '/period-paid', body);
                toast(val ? 'bezahlt' : 'offen', 'success'); render();
            } catch (err) { toast(err.message, 'error'); render(); }
        };
        const onPaid = async (e) => {
            markActive(e.currentTarget);
            const fixed = periodFixed(rc, key);
            if (running || fixed != null) {
                const current = fixed != null ? { mode: 'partial', amount: fixed } : (paid ? { mode: 'full' } : null);
                const choice = await periodPayDialog(key, defAmt, current);
                if (!choice) { render(); return; }
                // A partial payment of 0/blank is not a payment — revert instead of
                // submitting an empty Teilbetrag.
                if (choice.amount != null && !(choice.amount > 0)) { render(); return; }
                await post(true, choice.amount);
                return;
            }
            await post(true, null);
        };
        seg.append(el('button', { class: !paid ? 'active open' : '', type: 'button', role: 'radio', 'aria-checked': String(!paid), onclick: (e) => { markActive(e.currentTarget); post(false, null); } }, 'offen'));
        seg.append(el('button', { class: paid ? 'active done' : '', type: 'button', role: 'radio', 'aria-checked': String(paid), onclick: onPaid }, 'bezahlt'));
        return segThumb(seg);
    }
    async function recurringForm(personId, existing) {
        // Bindable vehicles: active + not archived, plus the charge's own bound one
        // so editing a charge on an archived vehicle keeps the binding.
        let vs = [];
        let vehLoadFailed = false;
        try { vs = await api.get('/vehicles?person_id=' + personId); } catch { vehLoadFailed = true; }
        const vehOpts = [{ value: '', label: '— frei (direkt bezahlbar) —' }];
        if (vehLoadFailed && existing?.vehicle_id != null) {
            // List unavailable: keep the current binding selectable so saving
            // unrelated edits never silently unbinds the charge.
            vehOpts.push({ value: existing.vehicle_id, label: (existing.vehicle_label || 'Aktuelles Gefährt') + ' · Liste nicht geladen' });
        } else {
            vehOpts.push(...vs.filter((v) => !v.archived || v.id === existing?.vehicle_id).map((v) => ({ value: v.id, label: vehicleTitle(v) + (v.archived ? ' · archiviert' : '') })));
        }
        await formModal({
            title: existing ? 'Wiederkehrende Kosten bearbeiten' : 'Neue wiederkehrende Kosten',
            submitLabel: 'Speichern',
            fields: [
                { name: 'description', label: 'Bezeichnung', required: true, value: existing?.description },
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', min: 0, required: true, value: existing?.amount ?? '' },
                { name: 'period', label: 'Abrechnung', type: 'select', value: existing?.period || 'monthly', options: [{ value: 'monthly', label: 'monatlich' }, { value: 'yearly', label: 'jährlich' }] },
                { name: 'start_date', label: 'Gültig ab', type: 'date', required: true, value: existing?.start_date ? existing.start_date.slice(0, 10) : today() },
                { name: 'end_date', label: 'Gültig bis (optional)', type: 'date', value: existing?.end_date ? existing.end_date.slice(0, 10) : '', help: 'Leer = laufend. Läuft je Zeitraum auf und ist je Zeitraum bezahlbar.' },
                { name: 'vehicle_id', label: 'Zuordnung (optional)', type: 'select', value: existing?.vehicle_id ?? '', options: vehOpts, help: 'An ein Gefährt binden → erscheint dort, Bezahlt läuft über Gefährt/Pauschale. Sonst frei markierbar.' },
            ],
            save: async (data) => {
                const payload = {
                    description: data.description, amount: Number(data.amount), period: data.period,
                    start_date: data.start_date, end_date: data.end_date === '' ? null : data.end_date,
                    vehicle_id: data.vehicle_id ? Number(data.vehicle_id) : null,
                };
                if (existing) await api.put('/recurring/' + existing.id, payload);
                else await api.post('/persons/' + personId + '/recurring', payload);
                toast('Wiederkehrende Kosten gespeichert', 'success'); render();
            },
        });
    }
    function delRecurring(rc, node) {
        deleteWithUndo('Wiederkehrende Kosten löschen?', `„${rc.description}“ wird entfernt.`,
            () => api.del('/recurring/' + rc.id), () => render(), node);
    }
    async function delAgreement(a) {
        const count = (a.vehicle_ids || []).length;
        let deleteVehicles = false;
        if (count > 0) {
            const choice = await agreementDeleteChoice(count);
            if (!choice) return; // cancelled
            deleteVehicles = choice === 'delete';
        } else if (!await confirmDialog('Pauschale löschen?', 'Der Pauschaleintrag wird entfernt.', 'Löschen')) {
            return;
        }
        try {
            await api.del('/agreements/' + a.id + (deleteVehicles ? '?delete_vehicles=true' : ''));
            toast(deleteVehicles ? 'Pauschale und Gefährte gelöscht' : 'Pauschale gelöscht', 'success'); render();
        } catch (e) { toast(e.message, 'error'); }
    }
    // Ask what to do with a Pauschale's bound vehicles on delete: keep them (they
    // become individually billed) or delete them too. Resolves 'keep' | 'delete' | null.
    function agreementDeleteChoice(count) {
        const g = (one, many) => (count === 1 ? one : many);
        return new Promise((resolve) => {
            let done = false;
            const finish = (v) => { if (!done) { done = true; resolve(v); } };
            const dlg = contentModal('Pauschale löschen?', (body, close) => {
                body.append(el('p', { class: 'danger-note' }, icon('alert', 16),
                    el('span', {}, `Der Pauschaleintrag wird entfernt. ${count} ${g('Gefährt ist', 'Gefährte sind')} zugeordnet.`)));
                const keep = el('button', { class: 'btn btn-ghost btn-block', style: 'margin-top:.7rem' }, 'Nur Pauschale löschen · Gefährte behalten');
                const del = el('button', { class: 'btn btn-danger btn-block', style: 'margin-top:.5rem' }, `Pauschale und ${count} ${g('Gefährt', 'Gefährte')} löschen`);
                keep.addEventListener('click', () => { finish('keep'); close(); });
                del.addEventListener('click', () => { finish('delete'); close(); });
                body.append(keep, del);
            }, { closeLabel: 'Abbrechen' });
            dlg.addEventListener('close', () => finish(null), { once: true });
        });
    }
    // Unified Pauschale dialog: edit the agreement terms AND manage its
    // subordinate vehicles (add new from a Tarif, bind an existing one, edit
    // type/label/plate, remove) in one place. Bound vehicles have no own price —
    // they are billed via the Pauschale — so a row only needs Tarif/Bezeichnung/
    // Kennzeichen. An empty list means the Pauschale covers all of the person's
    // vehicles.
    async function agreementForm(personId, vehicles, existing) {
        const rows = []; // { id|null, cat, label, plate, node }
        const boundInitially = (existing && existing.vehicle_ids) || [];
        let listBox, emptyNote, existSel;

        // Vehicles that can still be bound: active, not archived, not already a row
        // here, and not already covered by another Pauschale.
        const bindable = () => vehicles.filter((v) => !v.archived
            && (v.status === 'stored' || v.status === 'reserved')
            && !v.flat_rate_covered
            && !rows.some((r) => r.id === v.id));

        const refreshControls = () => {
            if (emptyNote) emptyNote.hidden = rows.length > 0;
            if (!existSel) return;
            const opts = bindable();
            existSel.innerHTML = '';
            existSel.append(el('option', { value: '' }, opts.length ? '+ vorhandenes Gefährt …' : 'keine weiteren Gefährte'));
            for (const v of opts) existSel.append(el('option', { value: v.id }, vehicleTitle(v)));
            existSel.disabled = !opts.length;
        };

        const addRow = (v) => {
            // A brand-new row needs an active tariff; existing bound vehicles keep
            // their own (possibly archived) one. Block a new row with no active tariff
            // rather than rendering an empty, unsubmittable selector.
            if (!v && !state.categories.some((c) => !c.archived)) {
                toast('Kein aktiver Tarif – zuerst einen reaktivieren oder anlegen', 'error');
                return;
            }
            const cat = el('select', {}, ...state.categories.filter((c) => !c.archived || (v && c.id === v.category_id)).map((c) => el('option', { value: c.id }, c.name)));
            if (v && v.category_id) for (const o of cat.options) if (Number(o.value) === v.category_id) o.selected = true;
            const label = el('input', { type: 'text', placeholder: 'Bezeichnung', value: (v && v.label) || '' });
            const plate = el('input', { type: 'text', placeholder: 'Kennzeichen', value: (v && v.license_plate) || '' });
            const entry = { id: v ? v.id : null, cat, label, plate };
            entry.node = el('div', { class: 'new-vehicle-row' }, cat, label, plate,
                el('button', { type: 'button', class: 'btn btn-ghost btn-sm', title: 'Entfernen',
                    onclick: () => { entry.node.remove(); const i = rows.indexOf(entry); if (i >= 0) rows.splice(i, 1); refreshControls(); } }, icon('close', 14)));
            rows.push(entry);
            listBox.append(entry.node);
            refreshControls();
        };

        const data = await formModal({
            title: existing ? 'Pauschale bearbeiten' : 'Neue Pauschale',
            submitLabel: 'Speichern',
            fields: [
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', min: 0.01, required: true, value: existing?.amount ?? '' },
                { name: 'period', label: 'Zeitraum', type: 'select', value: existing?.period || 'monthly', options: [{ value: 'monthly', label: 'pro Monat' }, { value: 'yearly', label: 'pro Jahr' }] },
                { name: 'start_date', label: 'Gültig ab', type: 'date', required: true, value: existing?.start_date ? existing.start_date.slice(0, 10) : today() },
                { name: 'end_date', label: 'Gültig bis (optional)', type: 'date', value: existing?.end_date ? existing.end_date.slice(0, 10) : '', help: 'Leer = laufend. Läuft die Pauschale aus, werden die Gefährte automatisch archiviert.' },
                { name: 'note', label: 'Notiz (optional)', value: existing?.note },
            ],
            onRender: (body) => {
                segmentedField(body, 'period', [{ v: 'monthly', l: 'pro Monat' }, { v: 'yearly', l: 'pro Jahr' }]);
                if (!state.categories.length) { body.append(el('div', { class: 'card-meta' }, 'Zuerst einen Tarif anlegen.')); return; }
                body.append(el('label', {}, 'Gefährte dieser Pauschale'));
                listBox = el('div', { class: 'new-vehicles' });
                body.append(listBox);
                emptyNote = el('div', { class: 'card-meta' }, 'Keine bestimmten Gefährte – die Pauschale deckt dann alle Gefährte der Person.');
                body.append(emptyNote);
                const controls = el('div', { class: 'controls-row' });
                controls.append(el('button', { type: 'button', class: 'btn btn-ghost btn-sm', onclick: () => addRow(null) }, '+ Neues Gefährt'));
                existSel = el('select', {});
                existSel.addEventListener('change', () => { const id = Number(existSel.value); if (id) { const v = vehicles.find((x) => x.id === id); if (v) addRow(v); } });
                controls.append(existSel);
                body.append(controls);
                body.append(el('div', { class: 'card-meta' }, 'Neue Gefährte werden aus dem Tarif angelegt; alle gelisteten Gefährte werden über diese Pauschale abgerechnet.'));
                // Prefill the already-bound vehicles as editable rows.
                for (const vid of boundInitially) addRow(vehicles.find((x) => x.id === vid) || { id: vid, category_id: state.categories[0].id });
                refreshControls();
            },
            // Save inside the modal so a server rejection (409/400) keeps the dialog
            // open with an inline error instead of closing and losing the operator's
            // input (incl. newly-added vehicle rows) — matching the other forms.
            save: async (data) => {
                const vehicleIDs = [];
                const newVehicles = [];
                const editVehicles = [];
                for (const r of rows) {
                    const catId = Number(r.cat.value);
                    if (!catId) continue;
                    const label = r.label.value.trim();
                    const plate = r.plate.value.trim();
                    if (r.id) { vehicleIDs.push(r.id); editVehicles.push({ id: r.id, category_id: catId, label, license_plate: plate }); }
                    else newVehicles.push({ category_id: catId, label, license_plate: plate });
                }
                const payload = {
                    amount: data.amount === '' ? null : Number(data.amount),
                    period: data.period,
                    start_date: data.start_date,
                    end_date: data.end_date === '' ? null : data.end_date,
                    note: data.note,
                    vehicle_ids: vehicleIDs,
                    new_vehicles: newVehicles,
                    edit_vehicles: editVehicles,
                };
                if (existing) await api.put('/agreements/' + existing.id, payload);
                else await api.post('/persons/' + personId + '/agreements', payload);
            },
        });
        if (!data) return; // cancelled
        toast('Pauschale gespeichert', 'success');
        render();
    }

    // Sliding thumb for seg-mini sliders: a coloured pill that glides to the
    // active segment instead of the colour jumping between buttons. The thumb
    // carries the colour (via the active button's status class); buttons only
    // switch their text colour.
    const THUMB_CLASSES = ['st-reserved', 'st-stored', 'st-collected', 'st-cancelled', 'open', 'done'];
    function moveThumb(seg) {
        const th = seg.querySelector('.seg-thumb');
        if (!th) return;
        const act = seg.querySelector('button.active');
        if (!act || !act.offsetWidth) { th.style.opacity = '0'; return; }
        th.style.opacity = '1';
        th.style.left = act.offsetLeft + 'px';
        th.style.width = act.offsetWidth + 'px';
        th.className = 'seg-thumb ' + THUMB_CLASSES.filter((c) => act.classList.contains(c)).join(' ');
    }
    // Position the thumb without animating — used when a slider first becomes
    // measurable (initial render, or a <details> section opening), so nothing
    // visibly slides into place.
    function placeThumb(seg) {
        const th = seg.querySelector('.seg-thumb');
        if (!th) return;
        th.style.transition = 'none';
        moveThumb(seg);
        void th.offsetWidth;
        th.style.transition = '';
    }
    // Roving tabindex: a radiogroup is a single tab stop, so only the active
    // option is Tab-reachable; arrow keys move between options. Keeps at least
    // one enabled button tabbable even when the active one is disabled.
    function segRoving(seg) {
        const btns = Array.from(seg.querySelectorAll('button'));
        const enabled = btns.filter((b) => !b.disabled);
        if (!enabled.length) return;
        const stop = enabled.find((b) => b.classList.contains('active')) || enabled[0];
        btns.forEach((b) => { b.tabIndex = b === stop ? 0 : -1; });
    }
    function onSegKeydown(e) {
        const dir = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1
            : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1
            : e.key === 'Home' ? 'first' : e.key === 'End' ? 'last' : 0;
        if (!dir) return;
        const list = Array.from(e.currentTarget.querySelectorAll('button')).filter((b) => !b.disabled);
        const i = list.indexOf(document.activeElement);
        if (i < 0 || list.length < 2) return;
        e.preventDefault();
        const j = dir === 'first' ? 0 : dir === 'last' ? list.length - 1
            : (i + dir + list.length) % list.length;
        // Move focus only; Space/Enter activates via the button's own onclick, so
        // arrowing through statuses doesn't fire an API call per keypress.
        list[j].focus();
    }
    function segThumb(seg) {
        seg.prepend(el('span', { class: 'seg-thumb', 'aria-hidden': 'true' }));
        segRoving(seg);
        seg.addEventListener('keydown', onSegKeydown);
        // Position once the slider is laid out (offsetWidth is 0 before).
        requestAnimationFrame(() => placeThumb(seg));
        return seg;
    }
    window.addEventListener('resize', () => $$('.seg-mini').forEach(moveThumb));

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
        return segThumb(seg);
    }
    function paidSlider(v) {
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus' });
        seg.append(el('button', { class: (!v.paid ? 'active open' : ''), type: 'button', role: 'radio', 'aria-checked': String(!v.paid), 'aria-label': 'Zahlung offen',
            onclick: (e) => { markActive(e.currentTarget); markPaid(v, false); } }, 'offen'));
        seg.append(el('button', { class: (v.paid ? 'active done' : ''), type: 'button', role: 'radio', 'aria-checked': String(v.paid), 'aria-label': 'Bezahlt',
            onclick: (e) => { markActive(e.currentTarget); markPaid(v, true); } }, 'bezahlt'));
        return segThumb(seg);
    }
    // Optimistic feedback: instantly reflect the picked segment before the API returns.
    function markActive(btn) {
        for (const sib of btn.parentElement.children) {
            if (sib.tagName !== 'BUTTON') continue;
            sib.classList.toggle('active', sib === btn);
            if (sib.hasAttribute('aria-checked')) sib.setAttribute('aria-checked', String(sib === btn));
        }
        moveThumb(btn.parentElement);
        segRoving(btn.parentElement); // keep the single tab stop on the new selection
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
        // The price is seeded from the Tarif but then locked onto the vehicle, so
        // a later Tarif change never re-prices it.
        const catDefault = (cid, period) => { const c = state.categories.find((x) => x.id === Number(cid)); return c ? (period === 'yearly' ? c.default_yearly_cost : c.default_monthly_cost) : 0; };
        const initCat = existing?.category_id ?? state.categories[0].id;
        const initPeriod = existing?.billing_period || 'monthly';
        const initRate = existing?.rate ?? catDefault(initCat, initPeriod);
        let customIcons = []; try { customIcons = await api.get('/planner-icons'); } catch (e) { /* optional */ }
        await formModal({
            title: existing ? 'Gefährt bearbeiten' : 'Neues Gefährt',
            fields: [
                { name: 'person_id', label: 'Person', type: 'select', required: true, value: existing?.person_id ?? presetPerson ?? state.persons[0].id, options: state.persons.map((p) => ({ value: p.id, label: personName(p) })) },
                { name: 'category_id', label: 'Tarif / Typ', type: 'select', required: true, value: initCat, options: state.categories.filter((c) => !c.archived || c.id === initCat).map((c) => ({ value: c.id, label: `${c.name} (${eur(c.default_monthly_cost)}/M, ${eur(c.default_yearly_cost)}/J)` + (c.archived ? ' · archiviert' : '') })) },
                { name: 'label', label: 'Bezeichnung', value: existing?.label, placeholder: 'z. B. Wohnwagen Hobby' },
                { name: 'license_plate', label: 'Kennzeichen', value: existing?.license_plate },
                { name: 'start_date', label: 'Einstelldatum', type: 'date', required: true, value: existing?.start_date ? existing.start_date.slice(0, 10) : today() },
                { name: 'end_date', label: 'Abholdatum (optional)', type: 'date', value: existing?.end_date ? existing.end_date.slice(0, 10) : '', help: 'Leer = noch eingelagert. Hier auch nachträglich änderbar.' },
                { name: 'billing_period', label: 'Abrechnung', type: 'select', value: initPeriod, options: [{ value: 'monthly', label: 'monatlich' }, { value: 'yearly', label: 'jährlich' }] },
                { name: 'rate', label: 'Preis (€)', type: 'number', step: '0.01', min: 0, required: true, value: initRate, help: 'Aus dem Tarif übernommen und fest hinterlegt. Eine spätere Tarifänderung ändert diesen Preis nicht.' },
                { name: 'notes', label: 'Notizen', type: 'textarea', value: existing?.notes },
                { name: 'needs_power', label: 'Ladebedarf (Strom) — E-Fahrzeug / Kühlung', type: 'checkbox', value: !!existing?.needs_power },
                { name: 'planner_symbol', label: 'Planer-Symbol', type: 'select', value: existing?.planner_symbol || '', help: 'Standard: automatisch aus dem Tarif/Typ. Optional ein festes Draufsicht-Symbol oder ein eigenes hochgeladenes Icon wählen (im Planer unter „Eigene Icons verwalten").', options: [{ value: '', label: 'Automatisch (aus Typ)' }].concat(['PKW', 'Motorrad', 'Transporter', 'Wohnmobil', 'Wohnwagen', 'Anhänger', 'Boot / Trailer', 'Traktor', 'Ladewagen', 'Rückewagen', 'Kipper'].map((k) => ({ value: k, label: k }))).concat(customIcons.map((ic) => ({ value: 'custom:' + ic.id, label: 'Eigenes: ' + ic.name }))) },
            ],
            onRender: (body) => {
                segmentedField(body, 'billing_period', [{ v: 'monthly', l: 'monatlich' }, { v: 'yearly', l: 'jährlich' }]);
                const rateInp = body.querySelector('#f_rate');
                const catInp = body.querySelector('#f_category_id');
                const perInp = body.querySelector('#f_billing_period');
                let manual = !!existing; // on edit, never auto-overwrite the locked price
                rateInp.addEventListener('input', () => { manual = true; });
                const sync = () => { if (!manual) rateInp.value = catDefault(catInp.value, perInp.value); };
                catInp.addEventListener('change', sync);
                perInp.addEventListener('change', sync);
            },
            save: async (data) => {
                const payload = {
                    person_id: Number(data.person_id), category_id: Number(data.category_id),
                    label: data.label, license_plate: data.license_plate, notes: data.notes, billing_period: data.billing_period,
                    rate: data.rate === '' ? null : Number(data.rate),
                    start_date: data.start_date,
                    // Abholdatum (end_date) is editable here so it can be corrected
                    // retroactively; status/reservation stay slider-managed.
                    status: existing?.status || 'stored',
                    end_date: data.end_date === '' ? null : data.end_date,
                    reserved_from: existing?.reserved_from ? existing.reserved_from.slice(0, 10) : null,
                    reserved_until: existing?.reserved_until ? existing.reserved_until.slice(0, 10) : null,
                    needs_power: !!data.needs_power,
                    planner_symbol: data.planner_symbol || null,
                };
                if (existing) await api.put('/vehicles/' + existing.id, payload);
                else await api.post('/vehicles', payload);
                toast('Gefährt gespeichert', 'success'); render();
            },
        });
    }
    function delVehicle(v, node) {
        deleteWithUndo('Gefährt löschen?', `„${v.label || v.category_name}“ wird gelöscht.`,
            () => api.del('/vehicles/' + v.id), () => render(), node);
    }

    // ---------- VEHICLE DETAIL ----------
    routes.vehicle = async (page, id) => {
        await refreshLookups();
        const [vehicles, photos, history, handovers] = await Promise.all([
            api.get('/vehicles'), api.get('/vehicles/' + id + '/photos'), api.get('/vehicles/' + id + '/history'),
            api.get('/vehicles/' + id + '/handovers').catch(() => null),
        ]);
        const v = vehicles.find((x) => x.id === id);
        if (!v) { page.innerHTML = ''; page.append(emptyState('car', 'Gefährt nicht gefunden.')); return; }
        const vc = coverage(v);
        // Extra costs bound to this Gefährt (one-off + recurring), shown here so the
        // full picture lives at the Gefährt, not only on the person page. A failed
        // fetch propagates to the route's error handling rather than silently
        // rendering an empty (and misleading) "no extra costs" state.
        const [pCharges, pRecurring] = await Promise.all([
            api.get('/charges?person_id=' + v.person_id),
            api.get('/persons/' + v.person_id + '/recurring'),
        ]);
        const vCharges = pCharges.filter((c) => c.vehicle_id === v.id);
        const vRecurring = pRecurring.filter((rc) => rc.vehicle_id === v.id);
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', onclick: () => navigate('vehicles'), 'aria-label': 'Zurück' }, '‹'),
            el('h2', { style: 'margin:0;flex:1' }, esc(vehicleTitle(v))),
            el('a', { class: 'btn btn-ghost btn-sm', href: '/api/vehicles/' + v.id + '/label', target: '_blank', rel: 'noopener', title: 'Druck-Label mit QR-Code' }, '🖶 Label'),
            statusBadge(v.status)));

        page.append(el('div', { class: 'card' },
            el('div', { class: 'balance' }, el('span', {}, 'Person'), el('span', {}, el('a', { href: '#/persons/' + v.person_id }, esc(v.person_name)))),
            el('div', { class: 'balance' }, el('span', {}, 'Typ / Tarif'), el('span', {}, esc(v.category_name))),
            el('div', { class: 'balance' }, el('span', {}, 'Kennzeichen'), el('span', {}, esc(v.license_plate || '–'))),
            el('div', { class: 'balance' }, el('span', {}, 'Preis'), el('span', { class: 'amt' }, eur(v.effective_rate) + (v.billing_period === 'yearly' ? ' /Jahr' : ' /Monat'))),
            el('div', { class: 'balance' }, el('span', {}, 'Zeitraum'), el('span', {}, fmtDate(v.start_date) + (v.end_date ? ' – ' + fmtDate(v.end_date) : ' – offen'))),
            el('div', { class: 'balance' }, el('span', {}, 'Standort'), el('span', {}, vehLocationChip(v))),
            v.reserved_from ? el('div', { class: 'balance' }, el('span', {}, 'Reservierung'), el('span', {}, fmtDate(v.reserved_from) + ' – ' + fmtDate(v.reserved_until))) : null,
            vc.covered ? el('div', { class: 'balance' }, el('span', {}, 'Pauschale'), el('span', {}, vc.active ? 'aktiv abgedeckt' : 'beendet')) : null,
            vc.settled
                ? el('div', { class: 'balance' }, el('strong', {}, 'Aufgelaufen'), el('strong', { class: 'amt', title: 'Abrechnung über Pauschale' }, 'über Pauschale'))
                : el('div', { class: 'balance' }, el('strong', {}, vc.covered ? 'Aufgelaufen (Rest)' : 'Aufgelaufen'),
                    el('strong', { class: 'amt' }, eur(vc.owed))),
            v.notes ? el('div', { class: 'card-meta', style: 'margin-top:.5rem' }, esc(v.notes)) : null));

        // Bound extra costs, with a subtotal — kept separate from the rent
        // "Aufgelaufen" above (they are billed on top, exactly like on the person page).
        if (vCharges.length || vRecurring.length) {
            let sub = 0;
            vCharges.forEach((c) => { sub += Number(c.total) || 0; });
            vRecurring.forEach((rc) => { sub += Number(rc.accrued) || 0; });
            const zk = el('div', { class: 'card' }, el('h3', {}, 'Zusatzkosten'));
            vRecurring.forEach((rc) => zk.append(recurringRow(v.person_id, rc)));
            if (vCharges.length) zk.append(financeList(vCharges));
            zk.append(el('div', { class: 'balance', style: 'margin-top:.6rem;border-top:1px dashed var(--border);padding-top:.6rem' },
                el('strong', {}, 'Zusatzkosten aufgelaufen'),
                el('strong', { class: 'amt', style: 'color:var(--accent-text)' }, eur(sub))));
            page.append(zk);
        }

        if (v.archived) {
            page.append(el('div', { class: 'card is-archived' },
                el('h3', {}, 'Archiviert'),
                el('p', { class: 'muted' }, 'Dieses Gefährt ist abgeschlossen und schreibgeschützt. Zum Bearbeiten zuerst reaktivieren.'),
                vehicleControls(v)));
        } else {
            // status & payment sliders
            if (canManage() || canBill()) {
                const ctrl = el('div', { class: 'card' }, el('h3', {}, 'Status & Zahlung'), vehicleControls(v));
                if (canManage() && v.status !== 'cancelled') {
                    ctrl.append(el('button', { class: 'btn btn-ghost btn-sm', style: 'margin-top:.7rem', onclick: () => changeStatus(v, 'cancelled') }, icon('close', 14), ' Stornieren'));
                }
                page.append(ctrl);
            }
            if (canManage()) {
                page.append(el('div', { class: 'page-head' }, el('h3', {}, 'Bearbeiten'),
                    el('button', { class: 'btn btn-ghost btn-sm', onclick: () => vehicleForm(v) }, icon('edit'), ' Bearbeiten')));
            }
        }

        // photos
        const photoCard = el('div', { class: 'card' });
        const ph = el('div', { class: 'page-head' }, el('h3', {}, 'Fotos'));
        if (canManage()) {
            const fileInput = el('input', { type: 'file', accept: 'image/jpeg,image/png', style: 'display:none' });
            fileInput.addEventListener('change', () => uploadPhoto(id, fileInput.files[0]));
            // Camera capture: on mobile the `capture` hint opens the rear camera
            // directly; on desktop it falls back to the normal file picker. Keep the
            // JPEG/PNG restriction the backend enforces (avoids HEIC/WEBP rejects).
            const camInput = el('input', { type: 'file', accept: 'image/jpeg,image/png', capture: 'environment', style: 'display:none' });
            camInput.addEventListener('change', () => uploadPhoto(id, camInput.files[0]));
            ph.append(
                el('button', { class: 'btn btn-primary btn-sm', onclick: () => fileInput.click() }, '+ Foto'),
                el('button', { class: 'btn btn-ghost btn-sm', title: 'Mit Kamera aufnehmen', onclick: () => camInput.click() }, icon('camera'), ' Kamera'),
                fileInput, camInput);
        }
        photoCard.append(ph);
        if (!photos.length) photoCard.append(el('p', { class: 'muted' }, 'Keine Fotos.'));
        else {
            const grid = el('div', { class: 'photo-grid' });
            for (const p of photos) {
                const img = el('img', { src: '/api/photos/' + p.id, alt: esc(p.filename), loading: 'lazy', onclick: () => lightbox(p.id) });
                const thumb = el('div', { class: 'photo-thumb' }, img);
                if (canManage()) thumb.append(el('button', { class: 'del', title: (p.filename || 'Foto') + ' löschen', 'aria-label': (p.filename || 'Foto') + ' löschen', onclick: () => delPhoto(p, thumb) }, icon('close', 12)));
                grid.append(thumb);
            }
            photoCard.append(grid);
        }
        page.append(photoCard);

        // Übergabeprotokolle (Zustand + Unterschrift bei Ein-/Auslagerung)
        const hoCard = el('div', { class: 'card' });
        const hoHead = el('div', { class: 'page-head' }, el('h3', {}, 'Übergabeprotokolle'));
        if (canManage()) hoHead.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => handoverForm(id) }, '+ Protokoll'));
        hoCard.append(hoHead);
        if (handovers === null) hoCard.append(el('p', { class: 'muted' }, 'Protokolle konnten nicht geladen werden.'));
        else if (!handovers.length) hoCard.append(el('p', { class: 'muted' }, 'Noch keine Protokolle.'));
        else {
            handovers.forEach((ho) => {
                const dirLbl = ho.direction === 'auslagerung' ? 'Auslagerung' : 'Einlagerung';
                const row = el('div', { class: 'pay-row card' },
                    el('div', { class: 'pay-main' },
                        el('div', { class: 'pay-method' }, dirLbl,
                            ho.has_signature ? el('span', { class: 'badge badge-active', style: 'margin-left:.4rem' }, 'signiert') : null),
                        el('div', { class: 'pay-date' }, fmtDateTime(ho.created_at)
                            + (ho.signer_name ? ' · ' + esc(ho.signer_name) : '')
                            + (ho.created_by ? ' · ' + esc(ho.created_by) : ''))),
                    el('div', { class: 'card-actions' },
                        el('a', { class: 'btn btn-ghost btn-sm', href: '/api/handovers/' + ho.id + '/pdf', target: '_blank', rel: 'noopener' }, icon('receipt', 14), ' PDF'),
                        canManage() ? el('button', { class: 'btn btn-ghost btn-sm', title: 'Protokoll löschen', 'aria-label': 'Protokoll löschen', onclick: (e) => delHandover(ho, e.currentTarget.closest('.pay-row')) }, icon('trash', 14)) : null));
                if (ho.notes) row.querySelector('.pay-main').append(el('div', { class: 'inv-bills' }, esc(ho.notes)));
                hoCard.append(row);
            });
        }
        page.append(hoCard);

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
        } catch (e) { toast(e.message, 'error'); render(); } // roll back the optimistic slider on a rejected write
    }
    async function uploadPhoto(vehicleId, file) {
        if (!file) return;
        const fd = new FormData(); fd.append('photo', file);
        try { await api.upload('/vehicles/' + vehicleId + '/photos', fd); toast('Foto hochgeladen', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delPhoto(p, node) {
        deleteWithUndo('Foto löschen?', 'Das Foto wird entfernt.', () => api.del('/photos/' + p.id), () => render(), node);
    }
    async function delHandover(ho, node) {
        if (!await confirmDialog('Protokoll löschen?', 'Das Übergabeprotokoll wird dauerhaft entfernt.', 'Löschen')) return;
        try { await api.del('/handovers/' + ho.id); if (node) node.remove(); toast('Protokoll gelöscht', 'success'); }
        catch (e) { toast(e.message || 'Löschen fehlgeschlagen', 'error'); }
    }
    // Übergabeprotokoll form: direction + condition notes + signer + a drawn signature.
    async function handoverForm(vehicleId) {
        let sigData = () => '';
        await formModal({
            title: 'Übergabeprotokoll',
            submitLabel: 'Speichern',
            fields: [
                { name: 'direction', label: 'Art', type: 'select', value: 'einlagerung',
                    options: [{ value: 'einlagerung', label: 'Einlagerung (Übernahme)' }, { value: 'auslagerung', label: 'Auslagerung (Rückgabe)' }] },
                { name: 'notes', label: 'Zustand / Anmerkungen', type: 'textarea', placeholder: 'z. B. Kratzer vorne links, Tank halb voll …' },
                { name: 'signer_name', label: 'Name (Unterschrift)', type: 'text', placeholder: 'Name der unterschreibenden Person' },
            ],
            onRender: (body) => {
                segmentedField(body, 'direction', [{ v: 'einlagerung', l: 'Einlagerung' }, { v: 'auslagerung', l: 'Auslagerung' }]);
                const wrap = el('div', { class: 'field' }, el('label', {}, 'Unterschrift'));
                const canvas = el('canvas', { width: 500, height: 180,
                    style: 'width:100%;height:180px;touch-action:none;border:1px solid var(--border);border-radius:8px;background:#fff;display:block' });
                const ctx = canvas.getContext('2d');
                ctx.lineWidth = 2.4; ctx.lineCap = 'round'; ctx.lineJoin = 'round'; ctx.strokeStyle = '#111';
                let drawing = false, dirty = false, lx = 0, ly = 0;
                const at = (e) => { const r = canvas.getBoundingClientRect(); return { x: (e.clientX - r.left) * (canvas.width / r.width), y: (e.clientY - r.top) * (canvas.height / r.height) }; };
                canvas.addEventListener('pointerdown', (e) => { drawing = true; dirty = true; const p = at(e); lx = p.x; ly = p.y; try { canvas.setPointerCapture(e.pointerId); } catch (_) {} });
                canvas.addEventListener('pointermove', (e) => { if (!drawing) return; const p = at(e); ctx.beginPath(); ctx.moveTo(lx, ly); ctx.lineTo(p.x, p.y); ctx.stroke(); lx = p.x; ly = p.y; });
                const stop = () => { drawing = false; };
                canvas.addEventListener('pointerup', stop);
                canvas.addEventListener('pointercancel', stop);
                // No pointerleave: pointer capture keeps the stroke going outside the
                // canvas until the pointer is released/cancelled.
                const clearBtn = el('button', { type: 'button', class: 'btn btn-ghost btn-sm', style: 'margin-top:.4rem',
                    onclick: () => { ctx.clearRect(0, 0, canvas.width, canvas.height); dirty = false; } }, 'Unterschrift löschen');
                wrap.append(canvas, clearBtn);
                body.append(wrap);
                sigData = () => (dirty ? canvas.toDataURL('image/png') : '');
            },
            save: async (d) => {
                await api.post('/vehicles/' + vehicleId + '/handovers', {
                    direction: d.direction, notes: d.notes || '', signer_name: d.signer_name || '', signature: sigData(),
                });
                toast('Protokoll gespeichert', 'success');
                render();
            },
        });
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
        const bound = it.vehicle_id != null;
        // A bound charge is settled by its vehicle's slider (vehicle_paid), its
        // covering Pauschale slider (which sets the charge's own paid flag), or a
        // fully-paid Rechnung (invoiced && !invoice_open — PayInvoices settles the
        // invoice, not the raw flag). Honour all three, else a paid extra lingers
        // as "offen".
        const invoiceSettled = bound && it.invoiced && !it.invoice_open;
        const paidEff = bound ? (!!it.paid || !!it.vehicle_paid || invoiceSettled) : !!it.paid;
        const title = it.description ? esc(it.description) : 'Zusatzkosten';
        const metaBits = [esc(it.person_name), fmtDate(it.charged_on)];
        if (it.quantity !== 1) metaBits.push(`${it.quantity}×${eur(it.amount)}`);
        if (it.note) metaBits.push(esc(it.note));
        // Paid status sits under the details on the left: a bound charge follows
        // its vehicle (read-only badge); a free charge gets its offen/bezahlt
        // slider. Keeping it left of the amount keeps the right rail slim.
        let statusEl;
        if (bound && it.invoiced) {
            // Billed on a Rechnung: mirror the vehicle's derived status exactly —
            // "fakturiert" while a covering invoice is open, "bezahlt · Rechnung" once
            // all are paid.
            statusEl = it.invoice_open
                ? el('span', { class: 'badge badge-collected', title: 'Fakturiert – über die Rechnung begleichen' }, 'fakturiert')
                : el('span', { class: 'badge badge-active', title: 'Über eine bezahlte Rechnung beglichen' }, 'bezahlt · Rechnung');
        } else if (bound) statusEl = el('span', { class: 'badge ' + (paidEff ? 'badge-active' : 'badge-ended'), title: 'über ' + esc(it.vehicle_label || 'Gefährt') }, (paidEff ? 'bezahlt' : 'offen') + ' · Gefährt');
        else if (canBill()) statusEl = chargePaidSlider(it);
        else statusEl = el('span', { class: 'badge ' + (paidEff ? 'badge-active' : 'badge-ended') }, paidEff ? 'bezahlt' : 'offen');
        // Right rail: just the amount and (for billers) the row actions.
        const side = el('div', { class: 'row-side' },
            el('div', { class: 'charge-amt' }, eur(it.total)));
        if (canBill()) side.append(el('div', { class: 'card-actions' },
            el('button', { class: 'btn btn-ghost btn-sm', title: (it.description || 'Position') + ' bearbeiten', 'aria-label': (it.description || 'Position') + ' bearbeiten', onclick: () => chargeForm(it.person_id, it) }, icon('edit')),
            el('button', { class: 'btn btn-ghost btn-sm', title: (it.description || 'Position') + ' löschen', 'aria-label': (it.description || 'Position') + ' löschen', onclick: (e) => delFinance(it, e.currentTarget.closest('.card')) }, icon('trash'))));
        return el('div', { class: 'card charge-card ' + (paidEff ? 'is-paid' : 'is-open') },
            el('div', { class: 'charge-row' },
                el('span', { class: 'charge-ic', 'aria-hidden': 'true' }, icon('receipt', 20)),
                el('div', { class: 'charge-main' },
                    el('h3', {}, title),
                    el('div', { class: 'card-meta' }, metaBits.join(' · ')),
                    el('div', { class: 'charge-status' }, statusEl)),
                side));
    }
    function chargePaidSlider(it) {
        const seg = el('div', { class: 'seg-mini pay', role: 'radiogroup', 'aria-label': 'Zahlstatus' });
        seg.append(el('button', { class: (!it.paid ? 'active open' : ''), type: 'button', role: 'radio', 'aria-checked': String(!it.paid), 'aria-label': 'Zahlung offen',
            onclick: (e) => { markActive(e.currentTarget); setChargePaid(it, false); } }, 'offen'));
        seg.append(el('button', { class: (it.paid ? 'active done' : ''), type: 'button', role: 'radio', 'aria-checked': String(it.paid), 'aria-label': 'Bezahlt',
            onclick: (e) => { markActive(e.currentTarget); setChargePaid(it, true); } }, 'bezahlt'));
        return segThumb(seg);
    }
    // Serialize rapid offen/bezahlt clicks: record the latest wanted state and,
    // if no save is in flight, drain toward it — so a click during an in-flight
    // request is never dropped and the server ends on the user's final choice.
    async function setChargePaid(it, paid) {
        it._want = paid;
        if (it._saving) return;
        it._saving = true;
        try {
            while (it._want !== !!it.paid) {
                const want = it._want;
                await api.post('/charges/' + it.id + '/paid', { paid: want });
                it.paid = want;
            }
            toast(it.paid ? 'Als bezahlt markiert' : 'Als offen markiert', 'success');
        } catch (e) { toast(e.message, 'error'); }
        it._saving = false;
        render();
    }
    function delFinance(it, node) {
        deleteWithUndo('Position löschen?', 'Der Eintrag wird entfernt.',
            () => api.del('/charges/' + it.id), () => render(), node);
    }

    // Turn a formModal <select name=field> into a Parkrr segmented pill (the tab
    // style) that drives the hidden select — so formModal still reads the value
    // and any change-listeners on the select keep firing. opts: [{ v, l }].
    function segmentedField(body, name, opts) {
        const sel = body.querySelector('#f_' + name);
        if (!sel) return;
        // Announce the visible, localized label ("Abrechnung"/"Zeitraum"), not the
        // internal field name, to screen readers.
        const lbl = body.querySelector('label[for="f_' + name + '"]');
        const ariaLabel = (lbl && lbl.textContent.trim()) || name;
        sel.style.display = 'none';
        const seg = el('div', { class: 'segments', role: 'group', 'aria-label': ariaLabel, style: 'margin-bottom:0' });
        const btns = [];
        opts.forEach((o) => {
            const b = el('button', {
                type: 'button', class: sel.value === o.v ? 'active' : '', 'aria-pressed': String(sel.value === o.v),
                onclick: () => {
                    sel.value = o.v;
                    btns.forEach((x) => { const on = x === b; x.classList.toggle('active', on); x.setAttribute('aria-pressed', String(on)); });
                    sel.dispatchEvent(new Event('change'));
                },
            }, o.l);
            btns.push(b); seg.append(b);
        });
        sel.after(seg);
    }

    async function chargeForm(presetPerson, existing) {
        if (!state.persons.length) { toast('Zuerst eine Person anlegen', 'error'); return; }
        const svcOptions = [{ value: '', label: '— frei —' }, ...state.services.filter((s) => !s.archived).map((s) => ({ value: String(s.id), label: `${s.name} (${eur(s.default_amount)})` }))];
        const initPerson = existing?.person_id ?? presetPerson ?? state.persons[0].id;
        // Bindable vehicles: active + not archived, plus the charge's own bound one
        // (keepId) so editing a charge on an archived vehicle keeps the binding.
        const vehOpts = async (pid, keepId) => {
            let vs = [];
            try { vs = await api.get('/vehicles?person_id=' + pid); } catch { /* ignore */ }
            return [{ value: '', label: '— frei (direkt bezahlbar) —' },
                ...vs.filter((v) => !v.archived || v.id === keepId).map((v) => ({ value: v.id, label: vehicleTitle(v) + (v.archived ? ' · archiviert' : '') }))];
        };
        const initVehOpts = await vehOpts(initPerson, existing?.vehicle_id);
        await formModal({
            title: existing ? 'Zusatzkosten bearbeiten' : 'Neue Zusatzkosten',
            fields: [
                { name: 'person_id', label: 'Person', type: 'select', required: true, value: initPerson, options: state.persons.map((p) => ({ value: p.id, label: personName(p) })) },
                { name: 'billing', label: 'Abrechnung', type: 'select', value: 'once', options: [{ value: 'once', label: 'einmalig' }, { value: 'monthly', label: 'monatlich' }, { value: 'yearly', label: 'jährlich' }] },
                { name: 'service', label: 'Aus Katalog', type: 'select', value: '', options: svcOptions, help: 'Optional – füllt Bezeichnung & Betrag vor.' },
                { name: 'description', label: 'Bezeichnung', required: true, value: existing?.description ?? '' },
                { name: 'amount', label: 'Betrag (€)', type: 'number', step: '0.01', required: true, value: existing?.amount ?? '' },
                { name: 'quantity', label: 'Menge', type: 'number', step: '0.5', value: existing?.quantity ?? '1' },
                { name: 'charged_on', label: 'Datum', type: 'date', value: existing?.charged_on ? existing.charged_on.slice(0, 10) : today() },
                { name: 'start_date', label: 'Gültig ab', type: 'date', value: today() },
                { name: 'end_date', label: 'Gültig bis (optional)', type: 'date', value: '', help: 'Leer = laufend. Läuft je Zeitraum auf und ist je Zeitraum bezahlbar.' },
                { name: 'vehicle_id', label: 'Zuordnung (optional)', type: 'select', value: existing?.vehicle_id ?? '', options: initVehOpts, help: 'An ein Gefährt binden → erscheint dort, Bezahlt läuft über Gefährt/Pauschale. Sonst frei markierbar.' },
            ],
            onRender: (body) => {
                // Billing mode toggles which fields apply: once → Menge/Datum/Zuordnung;
                // monthly/yearly → Gültig ab/bis (recurring, accrues per period).
                const setShown = (name, show) => {
                    const lbl = body.querySelector('label[for="f_' + name + '"]');
                    const inp = body.querySelector('#f_' + name);
                    const help = body.querySelector('#help_' + name);
                    const wrap = inp ? (inp.closest('.input-affix') || inp) : null;
                    for (const n of [lbl, wrap, help]) if (n) n.style.display = show ? '' : 'none';
                };
                const applyMode = (mode) => {
                    const once = mode === 'once';
                    // Zuordnung gilt für alle Abrechnungsarten (einmalig wie wiederkehrend).
                    setShown('quantity', once); setShown('charged_on', once); setShown('vehicle_id', true);
                    setShown('start_date', !once); setShown('end_date', !once);
                };
                const billing = body.querySelector('#f_billing');
                billing.addEventListener('change', () => applyMode(billing.value));
                // Present the billing type as a Parkrr segmented pill driving the
                // hidden select. Only for new positions — when editing a one-off the
                // type is fixed and the field stays hidden.
                if (!existing) {
                    segmentedField(body, 'billing', [{ v: 'once', l: 'einmalig' }, { v: 'monthly', l: 'monatlich' }, { v: 'yearly', l: 'jährlich' }]);
                }
                if (existing) { billing.value = 'once'; setShown('billing', false); } // editing a one-off: type fixed
                applyMode(billing.value);

                const svc = body.querySelector('#f_service');
                const desc = body.querySelector('#f_description');
                const amt = body.querySelector('#f_amount');
                // Picking a catalog entry fills Bezeichnung + Betrag right away (both
                // stay editable) — otherwise the required-field validation blocks
                // submit before the save-time fallback ever runs.
                svc.addEventListener('change', () => {
                    const s = state.services.find((x) => String(x.id) === svc.value);
                    if (!s) return;
                    desc.value = s.name; amt.value = s.default_amount;
                    for (const [node, errId] of [[desc, 'err_description'], [amt, 'err_amount']]) {
                        node.removeAttribute('aria-invalid');
                        const e = body.querySelector('#' + errId); if (e) e.hidden = true;
                    }
                });
                // Switching person reloads the bindable vehicles. A request token
                // guards against an earlier (slower) response overwriting a newer one.
                const per = body.querySelector('#f_person_id');
                const veh = body.querySelector('#f_vehicle_id');
                let vehReq = 0;
                per.addEventListener('change', async () => {
                    const my = ++vehReq;
                    veh.disabled = true; veh.innerHTML = '';
                    veh.append(el('option', { value: '' }, 'lädt …'));
                    // Back on the charge's original person, keep its (maybe archived) bound vehicle.
                    const keepId = String(per.value) === String(existing?.person_id) ? existing?.vehicle_id : null;
                    const opts = await vehOpts(per.value, keepId);
                    if (my !== vehReq) return; // superseded by a newer change
                    veh.innerHTML = '';
                    for (const o of opts) veh.append(el('option', { value: o.value }, o.label));
                    veh.disabled = false;
                });
            },
            save: async (data) => {
                // Fallback for a catalog pick left otherwise untouched.
                if (data.service) {
                    const s = state.services.find((x) => String(x.id) === String(data.service));
                    if (s) {
                        if (!data.description) data.description = s.name;
                        if (!data.amount) data.amount = s.default_amount;
                    }
                }
                const personID = Number(data.person_id);
                if (existing || data.billing === 'once') {
                    // One-off charge: same payload whether creating or editing.
                    const payload = {
                        person_id: personID, description: data.description, amount: Number(data.amount),
                        quantity: Number(data.quantity) || 1, charged_on: data.charged_on,
                        vehicle_id: data.vehicle_id ? Number(data.vehicle_id) : null,
                    };
                    if (existing) await api.put('/charges/' + existing.id, payload);
                    else await api.post('/charges', payload);
                } else {
                    // Recurring: monthly/yearly, accrues per period; optionally bound to
                    // a Gefährt (then settled via the Gefährt/Pauschale, like one-offs).
                    await api.post('/persons/' + personID + '/recurring', {
                        description: data.description, amount: Number(data.amount), period: data.billing,
                        start_date: data.start_date, end_date: data.end_date === '' ? null : data.end_date,
                        vehicle_id: data.vehicle_id ? Number(data.vehicle_id) : null,
                    });
                }
                toast('Zusatzkosten gespeichert', 'success'); render();
            },
        });
    }

    // ================= TARIFFS (categories + services) =================
    let tariffTab = 'categories';
    routes.tariffs = async (page) => {
        await refreshLookups();
        const isCat = tariffTab === 'categories';
        const manage = canManage();
        page.innerHTML = '';
        page.append(el('div', { class: 'page-head' }, el('h2', {}, 'Tarife & Dienste')));
        page.append(el('div', { class: 'segments' },
            el('button', { class: isCat ? 'active' : '', onclick: () => { tariffTab = 'categories'; render(); } }, 'Tarife'),
            el('button', { class: !isCat ? 'active' : '', onclick: () => { tariffTab = 'services'; render(); } }, 'Dienste')));
        page.append(el('p', { class: 'muted', style: 'margin:.2rem 0 .7rem' },
            isCat ? 'Zentrale Gefährt-Typen mit Standardpreisen (beim Gefährt überschreibbar).'
                : 'Katalog für Zusatzleistungen (Strom, Reinigung …).'));

        const list = el('div', {});
        // "+ Neu" opens a blank, already-expanded inline card at the top.
        if (manage) {
            const addBtn = el('button', { class: 'btn btn-ghost btn-block', style: 'margin-bottom:.6rem' },
                icon('plus', 15), isCat ? ' Neuer Tarif' : ' Neuer Dienst');
            addBtn.addEventListener('click', () => {
                // Keep at most one unsaved blank card, and clear the empty-state so
                // the new card doesn't sit next to "Noch keine …".
                const stale = list.querySelector('[data-new-card]'); if (stale) stale.remove();
                const empty = list.querySelector('.empty'); if (empty) empty.remove();
                cfgCloseAll(list);
                list.prepend(isCat ? categoryCard(null, true) : serviceCard(null, true));
            });
            page.append(addBtn);
        }
        const items = isCat ? state.categories : state.services;
        if (!items.length) list.append(emptyState(isCat ? 'tag' : 'receipt', isCat ? 'Noch keine Tarife.' : 'Noch keine Dienste.'));
        else if (isCat) items.forEach((c) => list.append(manage ? categoryCard(c) : cfgReadonly('tag', esc(c.name), 'Standardtarif',
            el('span', { style: 'display:flex;align-items:center;gap:.5rem' },
                c.rates_synced ? el('span', { class: 'badge badge-cat', title: 'Jahr = Monat × 12' }, '×12') : null,
                catRate(c)))));
        else items.forEach((s) => list.append(manage ? serviceCard(s) : cfgReadonly('receipt', esc(s.name), null,
            el('span', { class: 'cfg-right' }, eur(s.default_amount)))));
        page.append(list);
    };
    // Read-only config row (readers): no toggle, no edit.
    function cfgReadonly(iconName, title, sub, right) {
        return el('div', { class: 'card cfg-card' }, el('div', { class: 'cfg-head' },
            el('span', { class: 'cfg-ic' }, icon(iconName, 18)),
            el('span', { class: 'cfg-main' }, el('span', { class: 'cfg-title' }, title),
                sub ? el('span', { class: 'cfg-sub' }, sub) : null),
            right || null));
    }
    // Single-open accordion: collapse every open config card in a list.
    function cfgCloseAll(list) {
        for (const h of list.querySelectorAll('.cfg-head[aria-expanded="true"]')) {
            h.setAttribute('aria-expanded', 'false');
            const p = h.parentElement.querySelector('.tf-panel');
            if (p) { p.classList.remove('open'); p.inert = true; }
        }
    }
    // Discard an unsaved draft card; if it was the last card, restore the
    // empty-state (never duplicating it while persisted cards remain).
    function cfgDiscardDraft(card, iconName, text) {
        const list = card.parentElement;
        card.remove();
        if (list && !list.querySelector('.cfg-card') && !list.querySelector('.empty')) {
            list.append(emptyState(iconName, text));
        }
    }
    // Wire a config card's header/panel: collapsed panel is inert; opening one
    // collapses its siblings (single-open); optionally opens immediately.
    function cfgDisclosure(card, head, panel, openNow) {
        panel.inert = !openNow;
        if (openNow) { head.setAttribute('aria-expanded', 'true'); panel.classList.add('open'); }
        head.addEventListener('click', () => {
            const open = head.getAttribute('aria-expanded') === 'true';
            if (!open) cfgCloseAll(card.parentElement);
            head.setAttribute('aria-expanded', String(!open));
            panel.classList.toggle('open', !open);
            panel.inert = open;
        });
    }
    // Right-hand price for a Tarif card: monthly prominent, yearly muted below —
    // sits on the right like the Dienste amount, per the mockup.
    function catRate(c) {
        return el('span', { class: 'cfg-right cfg-rate' },
            el('span', { class: 'cfg-rate-m' }, eur(c.default_monthly_cost), el('span', { class: 'u' }, '/M')),
            el('span', { class: 'cfg-rate-y' }, eur(c.default_yearly_cost), el('span', { class: 'u' }, '/J')));
    }
    function categoryCard(c, openNow = false) {
        const pid = 'cat-panel-' + (c ? c.id : 'new');
        const head = el('button', { type: 'button', class: 'cfg-head tf-toggle', 'aria-expanded': 'false', 'aria-controls': pid },
            el('span', { class: 'cfg-ic' }, icon('tag', 18)),
            el('span', { class: 'cfg-main' },
                el('span', { class: 'cfg-title' }, c ? esc(c.name) : 'Neuer Tarif'),
                el('span', { class: 'cfg-sub' }, c ? 'Standardtarif' : 'noch nicht gespeichert')),
            c && c.archived ? el('span', { class: 'badge badge-collected' }, 'Archiviert') : null,
            c && c.rates_synced ? el('span', { class: 'badge badge-cat', title: 'Jahr = Monat × 12' }, '×12') : null,
            c ? catRate(c) : null,
            el('span', { class: 'tf-chev', 'aria-hidden': 'true' }, icon('chevron', 18)));

        const nameCatId = 'catname-' + (c ? c.id : 'new');
        const nameI = el('input', { type: 'text', id: nameCatId, value: c ? c.name : '' });
        const syncI = el('input', { type: 'checkbox' }); if (c && c.rates_synced) syncI.checked = true;
        const monI = el('input', { type: 'number', step: '0.01', min: 0, value: c ? c.default_monthly_cost : 0 });
        const yearI = el('input', { type: 'number', step: '0.01', min: 0, value: c ? c.default_yearly_cost : 0 });
        const r2 = (n) => Math.round(n * 100) / 100;
        // "koppeln" on: editing one price auto-fills the other (Jahr = Monat × 12).
        monI.addEventListener('input', () => { if (syncI.checked) yearI.value = r2((parseFloat(monI.value) || 0) * 12); });
        yearI.addEventListener('input', () => { if (syncI.checked) monI.value = r2((parseFloat(yearI.value) || 0) / 12); });
        syncI.addEventListener('change', () => { if (syncI.checked) yearI.value = r2((parseFloat(monI.value) || 0) * 12); });
        const syncId = 'cat-sync-' + (c ? c.id : 'new');
        syncI.id = syncId;

        const saveBtn = el('button', { class: 'btn btn-primary' }, icon('check', 15), ' Speichern');
        saveBtn.addEventListener('click', async () => {
            const name = nameI.value.trim();
            if (!name) { toast('Name ist erforderlich', 'error'); nameI.focus(); return; }
            saveBtn.disabled = true;
            const payload = { name, default_monthly_cost: Number(monI.value) || 0, default_yearly_cost: Number(yearI.value) || 0, rates_synced: syncI.checked };
            try {
                if (c) await api.put('/categories/' + c.id, payload); else await api.post('/categories', payload);
                toast('Tarif gespeichert', 'success'); render();
            } catch (e) { toast(e.message, 'error'); saveBtn.disabled = false; }
        });

        const saveRow = el('div', { class: 'cfg-save' }, saveBtn);
        const inner = el('div', { class: 'cfg-panel-in' },
            el('label', { for: nameCatId }, 'Name'), nameI,
            el('label', { class: 'switch', for: syncId }, syncI, el('span', { class: 'track' }), el('span', {}, 'Monats-/Jahrespreis koppeln (Jahr = Monat × 12)')),
            el('div', { class: 'field-row', style: 'margin-top:.5rem' },
                el('div', {}, el('label', {}, 'Preis / Monat (€)'), monI),
                el('div', {}, el('label', {}, 'Preis / Jahr (€)'), yearI)),
            saveRow);
        if (c) {
            inner.append(el('div', { class: 'cfg-actions2' }, c.archived
                ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => setCatArchived(c, false) }, icon('undo', 15), ' Reaktivieren')
                : el('button', { class: 'btn btn-ghost btn-sm', onclick: () => setCatArchived(c, true) }, icon('archive', 15), ' Archivieren')));
            inner.append(el('div', { class: 'tf-danger-label' }, icon('alert', 13), 'Gefahrenzone'));
            inner.append(el('div', { class: 'u-dz-actions' },
                el('button', { class: 'btn btn-danger btn-sm', onclick: (e) => delCategory(c, e.currentTarget.closest('.card')) }, icon('trash', 15), ' Tarif löschen')));
        }
        const panel = el('div', { class: 'tf-panel', id: pid }, inner);
        const card = el('div', { class: 'card cfg-card' + (c && c.archived ? ' cfg-arch' : '') }, head, panel);
        // Unsaved blank card: mark it (single-instance) and let it be discarded.
        if (!c) { card.setAttribute('data-new-card', ''); saveRow.append(el('button', { type: 'button', class: 'btn btn-ghost', onclick: () => cfgDiscardDraft(card, 'tag', 'Noch keine Tarife.') }, 'Abbrechen')); }
        cfgDisclosure(card, head, panel, openNow);
        if (openNow) setTimeout(() => nameI.focus(), 50);
        return card;
    }
    function delCategory(c, node) { deleteWithUndo('Tarif löschen?', `„${c.name}“ wird gelöscht.`, () => api.del('/categories/' + c.id), () => render(), node); }
    async function setCatArchived(c, archived) {
        try { await api.post('/categories/' + c.id + '/archived', { archived }); toast(archived ? 'Tarif archiviert' : 'Tarif reaktiviert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function serviceCard(s, openNow = false) {
        const pid = 'svc-panel-' + (s ? s.id : 'new');
        const head = el('button', { type: 'button', class: 'cfg-head tf-toggle', 'aria-expanded': 'false', 'aria-controls': pid },
            el('span', { class: 'cfg-ic' }, icon('receipt', 18)),
            el('span', { class: 'cfg-main' }, el('span', { class: 'cfg-title' }, s ? esc(s.name) : 'Neuer Dienst')),
            s && s.archived ? el('span', { class: 'badge badge-collected' }, 'Archiviert') : null,
            el('span', { class: 'cfg-right' }, s ? eur(s.default_amount) : ''),
            el('span', { class: 'tf-chev', 'aria-hidden': 'true' }, icon('chevron', 18)));
        const nameI = el('input', { type: 'text', id: 'svcname-' + (s ? s.id : 'new'), value: s ? s.name : '' });
        const amtI = el('input', { type: 'number', step: '0.01', min: 0, value: s ? s.default_amount : 0 });
        const saveBtn = el('button', { class: 'btn btn-primary' }, icon('check', 15), ' Speichern');
        saveBtn.addEventListener('click', async () => {
            const name = nameI.value.trim();
            if (!name) { toast('Name ist erforderlich', 'error'); nameI.focus(); return; }
            saveBtn.disabled = true;
            const payload = { name, default_amount: Number(amtI.value) || 0 };
            try {
                if (s) await api.put('/services/' + s.id, payload); else await api.post('/services', payload);
                toast('Dienst gespeichert', 'success'); render();
            } catch (e) { toast(e.message, 'error'); saveBtn.disabled = false; }
        });
        const saveRow = el('div', { class: 'cfg-save' }, saveBtn);
        const inner = el('div', { class: 'cfg-panel-in' },
            el('label', { for: nameI.id }, 'Name'), nameI,
            el('label', {}, 'Standardpreis (€)'), amtI,
            saveRow);
        if (s) {
            inner.append(el('div', { class: 'cfg-actions2' }, s.archived
                ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => setSvcArchived(s, false) }, icon('undo', 15), ' Reaktivieren')
                : el('button', { class: 'btn btn-ghost btn-sm', onclick: () => setSvcArchived(s, true) }, icon('archive', 15), ' Archivieren')));
            inner.append(el('div', { class: 'tf-danger-label' }, icon('alert', 13), 'Gefahrenzone'));
            inner.append(el('div', { class: 'u-dz-actions' },
                el('button', { class: 'btn btn-danger btn-sm', onclick: (e) => delService(s, e.currentTarget.closest('.card')) }, icon('trash', 15), ' Dienst löschen')));
        }
        const panel = el('div', { class: 'tf-panel', id: pid }, inner);
        const card = el('div', { class: 'card cfg-card' + (s && s.archived ? ' cfg-arch' : '') }, head, panel);
        // Unsaved blank card: mark it (single-instance) and let it be discarded.
        if (!s) { card.setAttribute('data-new-card', ''); saveRow.append(el('button', { type: 'button', class: 'btn btn-ghost', onclick: () => cfgDiscardDraft(card, 'receipt', 'Noch keine Dienste.') }, 'Abbrechen')); }
        cfgDisclosure(card, head, panel, openNow);
        if (openNow) setTimeout(() => nameI.focus(), 50);
        return card;
    }
    async function setSvcArchived(s, archived) {
        try { await api.post('/services/' + s.id + '/archived', { archived }); toast(archived ? 'Dienst archiviert' : 'Dienst reaktiviert', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    function delService(s, node) { deleteWithUndo('Dienst löschen?', `„${s.name}“ wird gelöscht.`, () => api.del('/services/' + s.id), () => render(), node); }

    // ================= USERS (admin) =================
    routes.users = async (page) => {
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('settings', 'Nur für Administratoren.')); return; }
        const users = await api.get('/users');
        mountList(page, {
            title: 'Benutzer', emptyIcon: 'users', emptyText: 'Keine Benutzer.',
            onAdd: () => userForm(), items: users,
            searchText: (u) => norm([u.username, u.email, ROLE_LABEL[u.role]].join(' ')),
            sorts: [{ label: 'Name A–Z', cmp: (a, b) => a.username.localeCompare(b.username) }],
            render: (u) => userCard(u),
        });
    };
    async function downloadBackup(btn) {
        const orig = btn.textContent;
        btn.disabled = true; btn.textContent = 'Sichere …';
        try {
            const res = await fetch('/api/backup', {
                method: 'POST',
                headers: { 'X-CSRF-Token': getCookie('parkrr_csrf') },
                credentials: 'same-origin',
            });
            if (!res.ok) {
                let msg = 'Backup fehlgeschlagen';
                try { const j = await res.json(); if (j && j.error) msg = j.error; } catch (e) { /* non-JSON */ }
                throw new Error(msg);
            }
            const blob = await res.blob();
            const cd = res.headers.get('Content-Disposition') || '';
            const m = cd.match(/filename="([^"]+)"/);
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url; a.download = m ? m[1] : 'parkrr-backup.dump.enc';
            document.body.append(a); a.click(); a.remove();
            URL.revokeObjectURL(url);
            toast('Backup heruntergeladen', 'success');
        } catch (e) {
            toast(e.message, 'error');
        } finally {
            btn.disabled = false; btn.textContent = orig;
        }
    }
    const fmtBytes = (n) => { n = Number(n) || 0; if (n < 1024) return n + ' B'; if (n < 1048576) return (n / 1024).toFixed(1) + ' KB'; return (n / 1048576).toFixed(1) + ' MB'; };

    // ---------- BACKUP (admin) ----------
    // ---- cron helpers (client twins of internal/backup/cron.go) ----
    const CRON_DOW = ['Sonntag', 'Montag', 'Dienstag', 'Mittwoch', 'Donnerstag', 'Freitag', 'Samstag'];
    const pad2 = (n) => String(n).padStart(2, '0');
    function cronFieldSet(f, lo, hi) {
        const out = new Set();
        for (const part of String(f).split(',')) {
            let step = 1, range = part;
            const sl = part.indexOf('/');
            if (sl >= 0) { step = parseInt(part.slice(sl + 1), 10); range = part.slice(0, sl); if (!(step >= 1)) return null; }
            let a, b;
            if (range === '*') { a = lo; b = hi; }
            else if (range.indexOf('-') >= 0) { const q = range.split('-'); a = parseInt(q[0], 10); b = parseInt(q[1], 10); }
            else { a = parseInt(range, 10); b = (sl >= 0) ? hi : a; }
            if (!Number.isInteger(a) || !Number.isInteger(b) || a < lo || b > hi || a > b) return null;
            for (let v = a; v <= b; v += step) out.add(v);
        }
        return out.size ? out : null;
    }
    function cronParse(expr) {
        const p = String(expr).trim().split(/\s+/);
        if (p.length !== 5) return null;
        const bounds = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 6]];
        const sets = [];
        for (let i = 0; i < 5; i++) { const s = cronFieldSet(p[i], bounds[i][0], bounds[i][1]); if (!s) return null; sets.push(s); }
        // robfig's ParseStandard treats "*" and "*/n" alike for the day-of-month
        // vs day-of-week AND/OR decision — mirror that so the preview matches.
        const starBit = (f) => /^\*(\/\d+)?$/.test(f);
        return { sets, domStar: starBit(p[2]), dowStar: starBit(p[4]) };
    }
    function cronNext(expr, from) {
        const c = cronParse(expr); if (!c) return null;
        const [minS, hrS, domS, monS, dowS] = c.sets;
        const d = new Date(from.getTime()); d.setSeconds(0, 0); d.setMinutes(d.getMinutes() + 1);
        for (let i = 0; i < 527040; i++) { // ~366 days of minutes
            if (minS.has(d.getMinutes()) && hrS.has(d.getHours()) && monS.has(d.getMonth() + 1)) {
                const domM = domS.has(d.getDate()), dowM = dowS.has(d.getDay());
                const dayOK = (c.domStar && c.dowStar) ? true : (c.domStar ? dowM : (c.dowStar ? domM : (domM || dowM)));
                if (dayOK) return new Date(d.getTime());
            }
            d.setMinutes(d.getMinutes() + 1);
        }
        return null;
    }
    function cronNextRuns(expr, k) {
        const out = []; let t = new Date();
        for (let i = 0; i < k; i++) { const n = cronNext(expr, t); if (!n) break; out.push(n); t = n; }
        return out;
    }
    function describeCron(expr) {
        expr = String(expr || '').trim();
        if (!expr) return 'Aus — kein automatisches Backup.';
        const p = expr.split(/\s+/);
        if (p.length !== 5 || !cronParse(expr)) return 'Ungültiger Cron-Ausdruck.';
        const [mi, hr, dom, mon, dow] = p;
        const isNum = (s) => /^\d+$/.test(s);
        const at = () => pad2(+hr) + ':' + pad2(+mi);
        if (mi === '0' && /^\*\/\d+$/.test(hr) && dom === '*' && mon === '*' && dow === '*') return 'Alle ' + hr.slice(2) + ' Stunden.';
        if (isNum(mi) && isNum(hr)) {
            if (dom === '*' && mon === '*' && dow !== '*' && isNum(dow)) return 'Jeden ' + CRON_DOW[((+dow % 7) + 7) % 7] + ' um ' + at() + ' Uhr.';
            if (dom !== '*' && mon === '*' && dow === '*' && isNum(dom)) return 'Monatlich am ' + (+dom) + '. um ' + at() + ' Uhr.';
            if (dom === '*' && mon === '*' && dow === '*') return 'Täglich um ' + at() + ' Uhr.';
        }
        return 'Cron: ' + expr;
    }
    const fmtWhen = (iso) => iso ? new Date(iso).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' }) : '—';

    // Parse a saved cron into the editor form (mode + fields); unrecognised shapes
    // fall back to the raw "Cron" mode so any expression stays editable.
    function cronToForm(expr) {
        expr = String(expr || '').trim();
        const f = { on: !!expr, mode: 'daily', time: '03:00', hours: 6, dow: '0', raw: expr };
        if (!expr) return f;
        f.mode = 'cron';
        const p = expr.split(/\s+/);
        if (p.length === 5 && cronParse(expr)) {
            const [mi, hr, dom, mon, dow] = p; const isNum = (s) => /^\d+$/.test(s);
            if (mi === '0' && /^\*\/\d+$/.test(hr) && dom === '*' && mon === '*' && dow === '*') { f.mode = 'hours'; f.hours = +hr.slice(2); }
            else if (isNum(mi) && isNum(hr) && dom === '*' && mon === '*' && dow !== '*' && isNum(dow)) { f.mode = 'weekly'; f.dow = dow; f.time = pad2(+hr) + ':' + pad2(+mi); }
            else if (isNum(mi) && isNum(hr) && dom === '*' && mon === '*' && dow === '*') { f.mode = 'daily'; f.time = pad2(+hr) + ':' + pad2(+mi); }
        }
        return f;
    }
    function formToCron(f) {
        if (!f.on) return '';
        if (f.mode === 'daily') { const [h, m] = f.time.split(':'); return (+m) + ' ' + (+h) + ' * * *'; }
        if (f.mode === 'hours') { return '0 */' + f.hours + ' * * *'; }
        if (f.mode === 'weekly') { const [h, m] = f.time.split(':'); return (+m) + ' ' + (+h) + ' * * ' + f.dow; }
        return String(f.raw || '').trim();
    }

    // ---------- BACKUP tab (admin) ----------
    const CRON_MODES = [['daily', 'Täglich'], ['hours', 'Alle N Std.'], ['weekly', 'Wöchentlich'], ['cron', 'Cron']];
    // A per-target schedule editor (Volume or S3): mode tabs + fields, a live cron
    // string, a plain-language description, the next runs, and a retention count.
    function scheduleColumn(label, iconKey, expr, keep, configured, note) {
        const f = cronToForm(expr);
        const keepIn = el('input', { type: 'number', min: '0', step: '1', value: String(keep ?? 0), 'aria-label': 'Behalten' });
        const controls = el('div', { class: 'sched-controls' });
        const cronOut = el('code', { class: 'sched-cron' });
        const descOut = el('div', { class: 'card-meta' });
        const runsOut = el('div', { class: 'sched-runs' });
        const tabsWrap = el('div', { class: 'seg-tabs', role: 'tablist', 'aria-label': label + ' Modus' });
        const bodyWrap = el('div', {});

        function renderRuns(cron) {
            runsOut.innerHTML = '';
            if (!cron) { runsOut.append(el('span', { class: 'card-meta' }, '—')); return; }
            const runs = cronNextRuns(cron, 3);
            if (!runs.length) { runsOut.append(el('span', { class: 'card-meta' }, 'Keine bevorstehenden Läufe.')); return; }
            runsOut.append(el('div', { class: 'card-meta', style: 'margin-bottom:.15rem' }, 'Nächste Läufe:'));
            for (const r of runs) runsOut.append(el('div', { class: 'sched-run' }, r.toLocaleString('de-DE', { weekday: 'short', day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })));
        }
        function refresh() {
            const cron = formToCron(f);
            cronOut.textContent = cron || '(aus)';
            descOut.textContent = describeCron(cron);
            renderRuns(cron);
        }
        function renderControls() {
            controls.innerHTML = '';
            if (!f.on) return;
            if (f.mode === 'weekly') {
                const sel = el('select', { 'aria-label': 'Wochentag' });
                CRON_DOW.forEach((d, i) => { const o = el('option', { value: String(i) }, d); if (String(i) === f.dow) o.selected = true; sel.append(o); });
                sel.addEventListener('change', () => { f.dow = sel.value; refresh(); });
                controls.append(el('label', { class: 'sched-lbl' }, 'Wochentag'), sel);
            }
            if (f.mode === 'daily' || f.mode === 'weekly') {
                const time = el('input', { type: 'time', value: f.time });
                time.addEventListener('input', () => { f.time = time.value || '03:00'; refresh(); });
                controls.append(el('label', { class: 'sched-lbl' }, 'Uhrzeit'), time);
            } else if (f.mode === 'hours') {
                const num = el('input', { type: 'number', min: '1', max: '24', value: String(f.hours) });
                num.addEventListener('input', () => { f.hours = Math.min(24, Math.max(1, +num.value || 1)); refresh(); });
                controls.append(el('label', { class: 'sched-lbl' }, 'Alle N Stunden'), num);
            } else {
                const raw = el('input', { type: 'text', value: f.raw || '', placeholder: '0 3 * * *', spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off' });
                // Debounce: a hand-typed expression can be valid-but-unsatisfiable
                // (e.g. "0 0 30 2 *"), making the next-runs scan walk up to a year —
                // don't run it on every keystroke.
                let rawTimer;
                raw.addEventListener('input', () => { f.raw = raw.value; clearTimeout(rawTimer); rawTimer = setTimeout(refresh, 200); });
                controls.append(el('label', { class: 'sched-lbl' }, 'Cron (Min Std Tag Mon Wtag)'), raw);
            }
        }
        function renderTabs() {
            tabsWrap.innerHTML = '';
            for (const [m, lbl] of CRON_MODES) {
                const b = el('button', { type: 'button', role: 'tab', class: 'seg-tab' + (f.mode === m ? ' active' : ''), 'aria-selected': String(f.mode === m) }, lbl);
                b.addEventListener('click', () => { if (m === 'cron' && !f.raw) f.raw = formToCron({ ...f, mode: (f.mode === 'cron' ? 'daily' : f.mode) }); f.mode = m; renderTabs(); renderControls(); refresh(); });
                tabsWrap.append(b);
            }
        }
        const toggle = el('input', { type: 'checkbox' });
        toggle.checked = f.on;
        function applyOn() { bodyWrap.style.display = f.on ? '' : 'none'; refresh(); }
        toggle.addEventListener('change', () => { f.on = toggle.checked; applyOn(); });

        bodyWrap.append(tabsWrap, controls,
            el('div', { class: 'sched-preview' }, cronOut, descOut, runsOut),
            el('div', { class: 'sched-keep' }, el('label', { class: 'sched-lbl' }, 'Behalten (Anzahl · 0 = alle)'), keepIn));
        renderTabs(); renderControls(); refresh(); applyOn();

        const col = el('div', { class: 'sched-col' + (configured ? '' : ' is-off') },
            el('div', { class: 'sched-col-head' },
                el('span', { class: 'sched-col-title' }, icon(iconKey, 16), ' ', el('b', {}, label)),
                el('label', { class: 'sched-switch' }, toggle, el('span', {}, 'Automatisch'))));
        if (!configured && note) col.append(el('div', { class: 'card-meta', style: 'margin:.2rem 0 .4rem' }, note));
        col.append(bodyWrap);
        return { node: col, read: () => ({ cron: formToCron(f), keep: Math.max(0, parseInt(keepIn.value, 10) || 0) }) };
    }
    function scheduleCard(st) {
        const s = st.settings || {};
        const volCol = scheduleColumn('Volume', 'archive', s.volume_cron || '', s.volume_keep ?? 14, !!st.scheduled,
            'Kein Volume gemountet — setze PARKRR_BACKUP_DIR. Der Zeitplan lässt sich trotzdem speichern.');
        const s3Col = scheduleColumn('S3', 'box', s.s3_cron || '', s.s3_keep ?? 0, !!st.s3,
            'Kein S3 konfiguriert — setze PARKRR_S3_* (Env). Der Zeitplan lässt sich trotzdem speichern.');
        const saveBtn = el('button', { class: 'btn btn-primary' }, 'Zeitplan speichern');
        const runBtn = el('button', { class: 'btn btn-ghost' }, 'Jetzt sichern');
        saveBtn.addEventListener('click', () => saveSchedule(volCol.read(), s3Col.read(), saveBtn));
        runBtn.addEventListener('click', () => runScheduledNow(runBtn));
        if (!(st.scheduled || st.s3)) runBtn.disabled = true;
        return el('div', { class: 'card', style: 'margin-top:1rem' },
            el('h3', {}, 'Zeitplan · Cron'),
            el('div', { class: 'card-meta', style: 'margin:.2rem 0 .7rem' }, 'Automatische verschlüsselte Sicherungen. Der Scheduler übernimmt Änderungen binnen einer Minute.'),
            el('div', { class: 'sched-grid' }, volCol.node, s3Col.node),
            el('div', { class: 'sched-actions' }, saveBtn, runBtn));
    }
    async function saveSchedule(vol, s3, btn) {
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Speichere …';
        try {
            await api.post('/backup/schedule', { volume_cron: vol.cron, volume_keep: vol.keep, s3_cron: s3.cron, s3_keep: s3.keep });
            toast('Zeitplan gespeichert', 'success');
        } catch (e) { toast(e.message, 'error'); }
        btn.disabled = false; btn.textContent = o;
    }
    async function runScheduledNow(btn) {
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Sichere …';
        try {
            const r = await api.post('/backup/run');
            const partial = r && r.partial;
            toast(partial ? 'Teilweise gesichert (' + ((r.ran || []).join(', ')) + ')' : 'Sicherung erstellt', partial ? 'error' : 'success');
            render();
        } catch (e) { toast(e.message, 'error'); btn.disabled = false; btn.textContent = o; }
    }
    function backupStatRow(st) {
        const s = st.status || {};
        return el('div', { class: 'stat-grid', style: 'margin-top:1rem' },
            stat(fmtWhen(s.last_volume_at), 'Letztes Volume-Backup', { icon: 'warehouse', tone: s.last_volume_ok ? 'green' : (s.last_volume_at ? 'amber' : undefined) }),
            stat(s.last_volume_size ? fmtBytes(s.last_volume_size) : '—', 'Größe', { icon: 'trend' }),
            stat(fmtWhen(s.restore_tested_at), 'Archiv geprüft', { icon: 'check', tone: s.restore_tested_at ? 'teal' : undefined }),
            stat(st.schema_version || '—', 'Schema-Stand', { icon: 'clock' }));
    }
    function backupFileRow(f, base, onRestore) {
        const row = el('div', { class: 'card-row', style: 'border-top:1px solid var(--border);padding:.45rem 0;align-items:center;gap:.4rem' },
            el('div', { style: 'flex:1;min-width:0' },
                el('div', { style: 'font-weight:600;font-size:.85rem;word-break:break-all' }, esc(f.name)),
                el('div', { class: 'card-meta' }, new Date(f.modified).toLocaleString('de-DE') + ' · ' + fmtBytes(f.size))),
            el('a', { class: 'btn btn-ghost btn-sm', href: base + encodeURIComponent(f.name), download: f.name }, 'Download'));
        if (onRestore) row.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: onRestore }, 'Restore'));
        return row;
    }
    routes.backup = async (page) => {
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('shield', 'Nur für Administratoren.')); return; }
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', onclick: () => navigate('dashboard') }, '‹'),
            el('h2', { style: 'margin:0' }, 'Backup')));
        let st = { enabled: false, scheduled: false, s3: false, dir: '', schema_version: '', settings: {}, status: {}, files: [], s3_files: [] };
        try { st = (await api.get('/backup/status')) || st; } catch (e) { /* keep defaults */ }

        if (!st.enabled) {
            page.append(el('div', { class: 'card' },
                el('h3', {}, 'Nicht aktiviert'),
                el('div', { class: 'card-meta', style: 'margin-top:.3rem' }, 'Setze die Umgebungsvariable PARKRR_BACKUP_KEY (AES-256-GCM-Schlüssel, getrennt vom Session-Secret), um Sicherungen zu erzeugen.')));
            return;
        }

        // Stat row
        page.append(backupStatRow(st));

        // On-demand download
        page.append(el('div', { class: 'card', style: 'margin-top:1rem' },
            el('h3', {}, 'Sofort-Sicherung'),
            el('div', { class: 'card-meta', style: 'margin:.3rem 0 .7rem' }, 'Verschlüsselter Voll-Export (pg_dump · AES-256-GCM) als Download. Off-box aufbewahren (2. Platte / NAS).'),
            el('button', { class: 'btn btn-primary', onclick: (e) => downloadBackup(e.currentTarget) }, 'Sofort herunterladen')));

        // Schedule (cron)
        page.append(scheduleCard(st));

        // Volume file list
        const sched = el('div', {});
        if (!st.scheduled) {
            sched.append(el('div', { class: 'card-meta' }, 'Kein Volume gemountet. Setze PARKRR_BACKUP_DIR (schreibbares Volume) für lokale Sicherungen mit Rotation.'));
        } else if (!st.files.length) {
            sched.append(el('div', { class: 'card-meta' }, 'Verzeichnis: ' + esc(st.dir) + ' — noch keine Dateien.'));
        } else {
            sched.append(el('div', { class: 'card-meta', style: 'margin-bottom:.5rem' }, 'Verzeichnis: ' + esc(st.dir)));
            sched.append(collapsibleRows(st.files, (f) => backupFileRow(f, '/api/backup/file/')));
        }
        page.append(el('div', { class: 'card', style: 'margin-top:1rem' }, el('h3', {}, 'Volume-Backups'), sched));

        // S3 (off-site)
        const s3box = el('div', {});
        if (!st.s3) {
            s3box.append(el('div', { class: 'card-meta' }, 'Nicht konfiguriert. Setze PARKRR_S3_ENDPOINT / _BUCKET / _ACCESS_KEY / _SECRET_KEY (S3-kompatibel: AWS S3, Backblaze B2, Wasabi, MinIO).'));
        } else {
            s3box.append(el('div', { class: 'card-row', style: 'align-items:center;margin-bottom:.5rem' },
                el('div', { style: 'flex:1;min-width:0' }, el('div', { class: 'card-meta' }, 'Bucket: ' + esc(st.s3_bucket))),
                el('button', { class: 'btn btn-ghost btn-sm', onclick: (e) => testS3Connection(e.currentTarget) }, 'Verbindung testen'),
                el('button', { class: 'btn btn-primary btn-sm', onclick: (e) => uploadToS3(e.currentTarget) }, 'Jetzt in S3 sichern')));
            if (!st.s3_files.length) s3box.append(el('div', { class: 'card-meta' }, 'Noch keine Objekte im Bucket.'));
            else s3box.append(collapsibleRows(st.s3_files, (f) => backupFileRow(f, '/api/backup/s3/file/', () => restoreFromS3(f.name))));
        }
        page.append(el('div', { class: 'card', style: 'margin-top:1rem' }, el('h3', {}, 'S3-Sicherung (extern)'), s3box));

        // Restore (upload + key + validate + confirmed, atomic restore)
        page.append(backupRestoreCard());

        // Key & notes
        page.append(el('div', { class: 'card', style: 'margin-top:1rem' },
            el('h3', {}, 'Schlüssel & Hinweise'),
            el('ul', { class: 'card-meta', style: 'margin:.4rem 0 0;padding-left:1.1rem;line-height:1.6' },
                el('li', {}, 'Der Verschlüsselungs-Schlüssel wird als Umgebungsvariable ', el('b', {}, 'PARKRR_BACKUP_KEY'), ' gesetzt (getrennt vom Session-Secret), nie in der App gespeichert.'),
                el('li', {}, 'Beim Wiederherstellen gibst du den Schlüssel erneut ein, der zu der jeweiligen Datei passt.'),
                el('li', {}, '„Archiv geprüft" heißt: das zuletzt geschriebene Volume-Backup wurde entschlüsselt und sein Inhaltsverzeichnis gelesen (pg_restore --list).'),
                el('li', {}, 'Für beliebig große Backups: Restore per Kommandozeile ', el('code', {}, 'parkrr restore <datei.dump.enc>'), '.'))));
    };
    function backupRestoreCard() {
        const fileIn = el('input', { type: 'file', accept: '.enc' });
        const keyIn = el('input', { type: 'password', placeholder: 'Backup-Schlüssel', autocomplete: 'off' });
        const confirmIn = el('input', { type: 'text', placeholder: 'RESTORE', autocomplete: 'off' });
        const result = el('div', { class: 'card-meta', style: 'margin:.5rem 0' });
        const restoreBtn = el('button', { class: 'btn btn-danger', disabled: true, onclick: (e) => restoreBackup(fileIn, keyIn, confirmIn, e.currentTarget) }, 'Wiederherstellen');
        const validateBtn = el('button', { class: 'btn btn-ghost', onclick: () => validateBackup(fileIn, keyIn, result, restoreBtn) }, 'Validieren');
        // Re-validation is required after any change, so the restore stays gated.
        const invalidate = () => { restoreBtn.disabled = true; result.textContent = ''; };
        fileIn.addEventListener('change', invalidate); keyIn.addEventListener('input', invalidate);
        return el('div', { class: 'card', style: 'margin-top:1rem' },
            el('h3', {}, 'Wiederherstellen'),
            el('div', { class: 'card-meta', style: 'margin:.3rem 0 .6rem;color:var(--accent-text);font-weight:600' },
                '⚠ Überschreibt die gesamte aktuelle Datenbank. Atomar (rollt bei Fehler komplett zurück). Erst validieren.'),
            el('label', {}, 'Backup-Datei (.dump.enc)'), fileIn,
            el('label', {}, 'Schlüssel'), keyIn,
            el('div', { style: 'margin-top:.6rem' }, validateBtn),
            result,
            el('label', {}, 'Zum Bestätigen RESTORE eingeben'), confirmIn,
            el('div', { style: 'margin-top:.6rem' }, restoreBtn));
    }
    async function validateBackup(fileIn, keyIn, result, restoreBtn) {
        if (!fileIn.files[0]) { toast('Bitte eine Backup-Datei wählen', 'error'); return; }
        if (!keyIn.value) { toast('Bitte den Schlüssel eingeben', 'error'); return; }
        result.textContent = 'Prüfe …';
        const fd = new FormData(); fd.append('file', fileIn.files[0]); fd.append('key', keyIn.value);
        try {
            const res = await fetch('/api/backup/validate', { method: 'POST', headers: { 'X-CSRF-Token': getCookie('parkrr_csrf') }, credentials: 'same-origin', body: fd });
            if (!res.ok) { const j = await res.json().catch(() => ({})); result.textContent = '✗ ' + (j.error || 'Ungültig'); restoreBtn.disabled = true; return; }
            const info = await res.json();
            result.textContent = '✓ Gültiges Backup · erstellt ' + (info.created || '?') + ' · ' + info.entries + ' Objekte';
            restoreBtn.disabled = false;
        } catch (e) { result.textContent = '✗ ' + e.message; restoreBtn.disabled = true; }
    }
    async function restoreBackup(fileIn, keyIn, confirmIn, btn) {
        if (confirmIn.value !== 'RESTORE') { toast('Zum Bestätigen RESTORE eingeben', 'error'); return; }
        if (!await confirmDialog('Datenbank überschreiben?', 'Die gesamte aktuelle Datenbank wird durch das Backup ersetzt. Das lässt sich nicht rückgängig machen. Du wirst danach neu angemeldet.', 'Wiederherstellen')) return;
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Stelle wieder her …';
        const fd = new FormData(); fd.append('file', fileIn.files[0]); fd.append('key', keyIn.value); fd.append('confirm', 'RESTORE');
        try {
            const res = await fetch('/api/backup/restore', { method: 'POST', headers: { 'X-CSRF-Token': getCookie('parkrr_csrf') }, credentials: 'same-origin', body: fd });
            if (!res.ok) { const j = await res.json().catch(() => ({})); throw new Error(j.error || 'Restore fehlgeschlagen'); }
            toast('Wiederhergestellt — bitte neu anmelden', 'success');
            setTimeout(() => logout(), 1800);
        } catch (e) { toast(e.message, 'error'); btn.disabled = false; btn.textContent = o; }
    }
    async function testS3Connection(btn) {
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Teste …';
        try {
            const res = await fetch('/api/backup/s3/test', { method: 'POST', headers: { 'X-CSRF-Token': getCookie('parkrr_csrf') }, credentials: 'same-origin' });
            const j = await res.json().catch(() => ({}));
            if (!res.ok) throw new Error(j.error || 'S3-Verbindung fehlgeschlagen');
            toast('S3-Verbindung ok — Bucket „' + (j.bucket || '') + '" erreichbar', 'success');
        } catch (e) { toast(e.message, 'error'); }
        finally { btn.disabled = false; btn.textContent = o; }
    }
    async function uploadToS3(btn) {
        const o = btn.textContent; btn.disabled = true; btn.textContent = 'Lade hoch …';
        try {
            const res = await fetch('/api/backup/s3', { method: 'POST', headers: { 'X-CSRF-Token': getCookie('parkrr_csrf') }, credentials: 'same-origin' });
            if (!res.ok) { const j = await res.json().catch(() => ({})); throw new Error(j.error || 'S3-Upload fehlgeschlagen'); }
            toast('In S3 gesichert', 'success'); render();
        } catch (e) { toast(e.message, 'error'); btn.disabled = false; btn.textContent = o; }
    }
    async function restoreFromS3(name) {
        const data = await formModal({
            title: 'Aus S3 wiederherstellen',
            submitLabel: 'Wiederherstellen',
            fields: [
                { name: 'key', label: 'Backup-Schlüssel', type: 'password', required: true },
                { name: 'confirm', label: 'Zum Bestätigen RESTORE eingeben', required: true, help: 'Überschreibt die gesamte Datenbank — atomar (rollt bei Fehler zurück).' },
            ],
        });
        if (!data) return;
        if (data.confirm !== 'RESTORE') { toast('Zum Bestätigen RESTORE eingeben', 'error'); return; }
        try {
            const res = await fetch('/api/backup/restore-s3', {
                method: 'POST',
                headers: { 'X-CSRF-Token': getCookie('parkrr_csrf'), 'Content-Type': 'application/x-www-form-urlencoded' },
                credentials: 'same-origin',
                body: new URLSearchParams({ name, key: data.key, confirm: 'RESTORE' }).toString(),
            });
            if (!res.ok) { const j = await res.json().catch(() => ({})); throw new Error(j.error || 'Restore fehlgeschlagen'); }
            toast('Wiederhergestellt — bitte neu anmelden', 'success');
            setTimeout(() => logout(), 1800);
        } catch (e) { toast(e.message, 'error'); }
    }
    // Expandable user card: name + role/2FA badges in the header; the panel holds a
    // quick password reset, a full edit, and a clearly separated "Gefahrenzone" for
    // the destructive actions (2FA reset, delete) so they can't be mis-tapped.
    function userCard(u) {
        const pid = 'u-panel-' + u.id;
        const isSelf = u.id === state.user.id;
        const initial = (u.username || '?').trim().charAt(0).toUpperCase() || '?';
        const head = el('button', { type: 'button', class: 'u-head tf-toggle', 'aria-expanded': 'false', 'aria-controls': pid },
            el('span', { class: 'u-avatar', 'aria-hidden': 'true' }, initial),
            el('span', { class: 'u-namewrap' },
                // A <span> (not a heading): interactive <button> content may only be
                // phrasing content, so a heading here would be invalid HTML.
                el('span', { class: 'u-name' }, esc(u.username), ' ',
                    el('span', { class: 'badge badge-role' }, ROLE_LABEL[u.role] || u.role),
                    u.totp_enabled ? el('span', { class: 'badge badge-stored badge-ic', title: '2FA aktiv', 'aria-label': '2FA aktiv' }, icon('shield', 12), '2FA') : null),
                el('span', { class: 'u-sub' }, esc(u.email) || 'keine E-Mail')),
            el('span', { class: 'tf-chev', 'aria-hidden': 'true' }, icon('chevron', 18)));

        // Inline quick password reset (keeps username/email/role untouched).
        const pw = el('input', { type: 'password', id: 'setpw-' + u.id, autocomplete: 'new-password' });
        const setBtn = el('button', { class: 'btn btn-primary' }, icon('key', 15), ' Setzen');
        setBtn.addEventListener('click', async () => {
            if (pw.value.length < 8) { toast('Passwort: mindestens 8 Zeichen', 'error'); pw.focus(); return; }
            setBtn.disabled = true;
            try {
                await api.put('/users/' + u.id, { username: u.username, email: u.email, role: u.role, password: pw.value });
                pw.value = ''; toast('Passwort gesetzt', 'success');
            } catch (e) { toast(e.message, 'error'); }
            setBtn.disabled = false;
        });

        const dz = el('div', { class: 'u-dz-actions' });
        if (u.totp_enabled) dz.append(el('button', { class: 'btn btn-ghost btn-sm', onclick: () => resetUserMfa(u) }, icon('unlock', 15), ' 2FA zurücksetzen'));
        if (!isSelf) dz.append(el('button', { class: 'btn btn-danger btn-sm', onclick: (e) => delUser(u, e.currentTarget.closest('.card')) }, icon('trash', 15), ' Benutzer löschen'));
        const hasDanger = u.totp_enabled || !isSelf;

        const panel = el('div', { class: 'tf-panel', id: pid },
            el('div', { class: 'u-panel-inner' },
                el('label', { class: 'u-pw-label', for: 'setpw-' + u.id }, 'Neues Passwort'),
                el('div', { class: 'u-pw-row' }, el('div', { class: 'input-affix' }, pw, pwToggleBtn(pw)), setBtn),
                el('button', { class: 'btn btn-ghost btn-sm u-edit', onclick: () => userForm(u) }, icon('edit', 15), ' Name, E-Mail & Rolle'),
                hasDanger ? el('div', { class: 'tf-danger-label' }, icon('alert', 13), 'Gefahrenzone') : null,
                hasDanger ? dz : null));

        const card = el('div', { class: 'card u-card' }, head, panel);
        panel.inert = true; // collapsed panel: keep its controls out of the tab order
        head.addEventListener('click', () => {
            const open = head.getAttribute('aria-expanded') === 'true';
            head.setAttribute('aria-expanded', String(!open));
            panel.classList.toggle('open', !open);
            panel.inert = open;
        });
        return card;
    }
    async function resetUserMfa(u) {
        if (!await confirmDialog('2FA zurücksetzen?', `Für „${u.username}" wird die Zwei-Faktor-Authentifizierung deaktiviert und alle Recovery-Codes gelöscht. Der Benutzer kann sich dann ohne 2FA anmelden und es neu einrichten.`, 'Zurücksetzen')) return;
        try { await api.post('/users/' + u.id + '/reset-2fa'); toast('2FA zurückgesetzt', 'success'); render(); }
        catch (e) { toast(e.message, 'error'); }
    }
    async function userForm(existing) {
        await formModal({
            title: existing ? 'Benutzer bearbeiten' : 'Neuer Benutzer',
            fields: [
                { name: 'username', label: 'Benutzername', required: !existing, value: existing?.username },
                { name: 'email', label: 'E-Mail', type: 'email', value: existing?.email },
                { name: 'password', label: existing ? 'Neues Passwort (optional)' : 'Passwort', type: 'password', required: !existing, minLength: 8, help: 'Mindestens 8 Zeichen.' },
                { name: 'role', label: 'Rolle', type: 'select', value: existing?.role || 'editor', options: Object.entries(ROLE_LABEL).map(([v, l]) => ({ value: v, label: l })) },
            ],
            save: async (data) => {
                const payload = { username: data.username, email: data.email, role: data.role, password: data.password };
                existing ? await api.put('/users/' + existing.id, payload) : await api.post('/users', payload);
                toast('Benutzer gespeichert', 'success'); render();
            },
        });
    }
    function delUser(u, node) { deleteWithUndo('Benutzer löschen?', `„${u.username}“ wird gelöscht.`, () => api.del('/users/' + u.id), () => render(), node); }

    // ================= AUDIT (admin) =================
    const AUDIT_ACTIONS = { create: 'Erstellt', update: 'Geändert', delete: 'Gelöscht', login: 'Login', logout: 'Logout' };
    const AUDIT_ENTITIES = { person: 'Person', vehicle: 'Gefährt', category: 'Tarif', service: 'Dienst', charge: 'Zusatzkosten', payment: 'Zahlung', invoice: 'Rechnung', billing: 'Rechnungs-Einstellungen', user: 'Benutzer', photo: 'Foto', passkey: 'Passkey' };

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
        if (!isAdmin()) { page.innerHTML = ''; page.append(emptyState('settings', 'Nur für Administratoren.')); return; }
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' }, el('button', { class: 'back-btn', onclick: () => navigate('dashboard') }, '‹'), el('h2', { style: 'margin:0' }, 'Audit-Log')));

        const q = { text: '', action: '', entity: '', from: '', to: '', offset: 0, limit: 50 };
        const search = el('input', { type: 'search', placeholder: 'Suchen (Benutzer, Beschreibung)…', 'aria-label': 'Audit-Log durchsuchen' });
        const optionList = (map, allLabel) => [el('option', { value: '' }, allLabel), ...Object.entries(map).map(([v, l]) => el('option', { value: v }, l))];
        const actSel = el('select', { 'aria-label': 'Aktion filtern' }, ...optionList(AUDIT_ACTIONS, 'Alle Aktionen'));
        const entSel = el('select', { 'aria-label': 'Objekt filtern' }, ...optionList(AUDIT_ENTITIES, 'Alle Objekte'));
        const fromIn = el('input', { type: 'date', 'aria-label': 'Von-Datum', title: 'Von' });
        const toIn = el('input', { type: 'date', 'aria-label': 'Bis-Datum', title: 'Bis' });
        page.append(el('div', { class: 'card audit-filters' }, search,
            el('div', { class: 'audit-filter-row' }, actSel, entSel),
            el('div', { class: 'audit-filter-row' }, fromIn, toIn)));

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
            if (q.from) p.set('from', q.from);
            if (q.to) p.set('to', q.to);
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
        fromIn.addEventListener('change', () => { q.from = fromIn.value; load(true); });
        toIn.addEventListener('change', () => { q.to = toIn.value; load(true); });
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

        // 2FA — header (icon + title + status badge), then body per state.
        const twoFA = el('div', { class: 'card tf' });
        const tfHead = (badge) => el('div', { class: 'tf-head' },
            el('span', { class: 'tf-head-ic' }, icon('shield', 22)),
            el('h3', {}, 'Zwei-Faktor'), badge);
        if (state.user.totp_enabled) {
            let remaining = null;
            try { remaining = (await api.get('/auth/2fa/backup-codes')).remaining; } catch { /* ignore */ }
            const low = remaining != null && remaining <= 2;
            twoFA.append(tfHead(el('span', { class: 'badge badge-stored tf-status' }, '✓ Aktiv')));
            twoFA.append(el('p', { class: 'tf-desc' }, 'Beim Anmelden brauchst du zusätzlich den 6-stelligen Code deiner Authenticator-App (oder einen Wiederherstellungscode).'));

            // Recovery-codes row: click to reveal the regenerate panel below
            const recToggle = el('button', { type: 'button', class: 'tf-item tf-toggle', 'aria-expanded': 'false', 'aria-controls': 'tf-regen-panel' },
                el('span', { class: 'tf-item-ic' }, icon('key', 18)),
                el('div', { class: 'tf-item-txt' },
                    el('div', { class: 'tf-item-title' }, 'Wiederherstellungscodes'),
                    el('div', { class: 'tf-item-sub' }, 'Ersatzcodes für den Notfall')),
                el('span', { class: 'badge ' + (low ? 'badge-ended' : 'badge-cat') },
                    remaining != null ? `${remaining} übrig` : '–'),
                el('span', { class: 'tf-chev', 'aria-hidden': 'true' }, icon('chevron', 18)));
            twoFA.append(recToggle);
            if (low) twoFA.append(el('p', { class: 'form-error', style: 'margin:.4rem 0 0' }, 'Wenige Codes übrig – bitte neu erstellen.'));

            // Collapsible regenerate panel: confirm password, then create new codes
            const regenPw = el('input', { id: 'tf-regen-pw', type: 'password', autocomplete: 'current-password' });
            const regenBtn = el('button', { class: 'btn btn-primary btn-block' }, 'Neue Codes erstellen');
            const regenPanel = el('div', { class: 'tf-panel', id: 'tf-regen-panel', role: 'region', 'aria-label': 'Neue Codes erstellen' },
                el('div', { class: 'tf-action' },
                    el('label', { class: 'tf-action-label', for: 'tf-regen-pw' }, 'Passwort bestätigen ', el('span', { class: 'req', 'aria-hidden': 'true' }, '*')),
                    el('div', { class: 'input-affix' }, regenPw, pwToggleBtn(regenPw)),
                    regenBtn));
            twoFA.append(regenPanel);
            regenPanel.inert = true; // collapsed panel: keep its controls out of the tab order
            let regenFocusTimer = null;
            recToggle.addEventListener('click', () => {
                const open = recToggle.getAttribute('aria-expanded') === 'true';
                recToggle.setAttribute('aria-expanded', String(!open));
                regenPanel.classList.toggle('open', !open);
                regenPanel.inert = open; // interactive only while expanded
                clearTimeout(regenFocusTimer);
                if (!open) regenFocusTimer = setTimeout(() => {
                    if (recToggle.getAttribute('aria-expanded') === 'true') regenPw.focus();
                }, 160);
            });
            regenBtn.addEventListener('click', async () => {
                if (!regenPw.value) { toast('Bitte Passwort eingeben', 'error'); regenPw.focus(); return; }
                regenBtn.disabled = true;
                try {
                    const res = await api.post('/auth/2fa/backup-codes/regenerate', { password: regenPw.value });
                    regenPw.value = '';
                    toast('Neue Recovery-Codes erstellt', 'success');
                    showBackupCodes(res.backup_codes || []);
                } catch (e) { toast(e.message, 'error'); regenBtn.disabled = false; }
            });

            // Danger zone
            twoFA.append(el('div', { class: 'tf-danger-label' }, icon('alert', 13), 'Gefahrenzone'));
            twoFA.append(el('button', { type: 'button', class: 'tf-danger-row', onclick: disable2FA },
                el('span', { class: 'tf-item-ic' }, icon('power', 18)),
                el('div', { class: 'tf-item-txt' },
                    el('div', { class: 'tf-item-title' }, '2FA deaktivieren'),
                    el('div', { class: 'tf-item-sub' }, 'Entfernt den zweiten Faktor von deinem Konto')),
                el('span', { class: 'tf-chev', 'aria-hidden': 'true' }, icon('chevron', 18))));
        } else {
            twoFA.append(tfHead(el('span', { class: 'badge badge-cat tf-status' }, 'Inaktiv')));
            twoFA.append(el('p', { class: 'tf-desc' }, 'Schütze dein Konto mit einer Authenticator-App: beim Anmelden ist dann zusätzlich ein 6-stelliger Code nötig.'));
            twoFA.append(el('button', { class: 'btn btn-primary btn-block', onclick: setup2FA }, '2FA einrichten'));
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
        await formModal({
            title: 'Passwort ändern', submitLabel: 'Ändern',
            fields: [
                { name: 'current_password', label: 'Aktuelles Passwort', type: 'password', required: true },
                { name: 'new_password', label: 'Neues Passwort', type: 'password', required: true, minLength: 8, help: 'Mindestens 8 Zeichen.' },
                { name: 'confirm_password', label: 'Neues Passwort bestätigen', type: 'password', required: true },
            ],
            // Live-check that both new-password entries match; block submit on a
            // mismatch so a typo can't set an unknown password (lockout).
            onRender: (body) => {
                const np = body.querySelector('#f_new_password');
                const cp = body.querySelector('#f_confirm_password');
                const err = body.querySelector('#err_confirm_password');
                const check = () => {
                    const mismatch = cp.value.length > 0 && np.value !== cp.value;
                    if (err) { err.textContent = mismatch ? 'Passwörter stimmen nicht überein' : ''; err.hidden = !mismatch; }
                    cp.setAttribute('aria-invalid', mismatch ? 'true' : 'false');
                    $('#modal-submit').disabled = mismatch;
                };
                np.addEventListener('input', check);
                cp.addEventListener('input', check);
            },
            save: async (data) => {
                await api.post('/auth/change-password', { current_password: data.current_password, new_password: data.new_password });
                toast('Passwort geändert', 'success');
            },
        });
    }
    async function setup2FA() {
        let info;
        try { info = await api.post('/auth/2fa/setup'); } catch (e) { toast(e.message, 'error'); return; }
        contentModal('2FA einrichten', (body, close) => {
            body.append(el('div', { class: 'setup-step' }, el('span', { class: 'step-n' }, '1'),
                el('div', {}, el('div', {}, 'In der Authenticator-App scannen'),
                    el('div', { class: 'field-hint' }, 'oder das Secret manuell eintragen'))));
            body.append(el('div', { class: 'qr-box' }, el('img', { src: info.qr, alt: 'QR-Code für die Authenticator-App' })));
            body.append(el('div', { class: 'secret secret-copy' }, el('span', {}, info.secret),
                el('button', { type: 'button', class: 'linkbtn', onclick: () => copyToClipboard(info.secret, 'Secret kopiert') }, 'Kopieren')));
            body.append(el('div', { class: 'setup-step', style: 'margin-top:.9rem' }, el('span', { class: 'step-n' }, '2'),
                el('label', { for: 'totp-enable', style: 'margin:0' }, '6-stelligen Code aus der App eingeben')));
            const inp = el('input', { id: 'totp-enable', type: 'text', inputmode: 'numeric', maxlength: 6, placeholder: '123456' });
            body.append(inp);
            const btn = el('button', { class: 'btn btn-primary btn-block', style: 'margin-top:.8rem' }, 'Aktivieren');
            btn.addEventListener('click', async () => {
                try {
                    const res = await withStepUp((pw) => api.post('/auth/2fa/enable', { code: inp.value, password: pw }));
                    toast('2FA aktiviert', 'success');
                    close();
                    state.user.totp_enabled = true;
                    showBackupCodes(res.backup_codes || []);
                } catch (e) { if (e && e.cancelled) return; toast(e.message, 'error'); }
            });
            body.append(btn);
        });
    }
    // Show one-time backup/recovery codes once, with copy & download. "Erledigt"
    // is gated behind an explicit acknowledgement so the codes — shown only now —
    // can't be dismissed by an accidental tap.
    function showBackupCodes(codes) {
        let chk; // acknowledgement checkbox — gates every dismissal path
        contentModal('Backup-Codes', (body, close) => {
            body.append(el('p', { class: 'backup-warn' }, icon('alert', 15), el('span', {}, 'Wird nur jetzt angezeigt – sichere die Codes, bevor du fortfährst.')));
            body.append(el('p', { class: 'muted' }, 'Jeder Einmal-Code funktioniert einmal, falls du keinen Authenticator zur Hand hast.'));
            const grid = el('div', { class: 'backup-codes' });
            for (const c of codes) grid.append(el('code', {}, c));
            body.append(grid);
            const text = codes.join('\n');
            const row = el('div', { style: 'display:flex;gap:.5rem;margin-top:.9rem' });
            row.append(el('button', { type: 'button', class: 'btn btn-ghost btn-sm', onclick: () => copyToClipboard(text, 'Kopiert') }, 'Kopieren'));
            row.append(el('button', { type: 'button', class: 'btn btn-ghost btn-sm', onclick: () => downloadText('parkrr-backup-codes.txt', text) }, 'Herunterladen'));
            body.append(row);
            const done = el('button', { type: 'button', class: 'btn btn-primary btn-block', style: 'margin-top:.9rem', disabled: true, onclick: () => { close(); render(); } }, 'Erledigt');
            chk = el('input', { type: 'checkbox', id: 'bc-saved' });
            chk.addEventListener('change', () => { done.disabled = !chk.checked; });
            body.append(el('label', { class: 'ack-check', for: 'bc-saved' }, chk, el('span', {}, 'Ich habe die Codes sicher gespeichert.')));
            body.append(done);
        }, { canClose: () => !!(chk && chk.checked), hideCancel: true });
    }
    function downloadText(filename, text) {
        const a = el('a', { href: 'data:text/plain;charset=utf-8,' + encodeURIComponent(text), download: filename });
        document.body.append(a); a.click(); a.remove();
    }
    // Copy to the clipboard, reporting success only once the write resolves and
    // an error when the API is unavailable or the write is rejected.
    function copyToClipboard(text, okMsg) {
        if (!navigator.clipboard) { toast('Kopieren wird hier nicht unterstützt', 'error'); return; }
        navigator.clipboard.writeText(text)
            .then(() => toast(okMsg, 'success'))
            .catch(() => toast('Kopieren fehlgeschlagen', 'error'));
    }
    async function disable2FA() {
        await formModal({
            title: '2FA deaktivieren', submitLabel: 'Deaktivieren',
            danger: 'Der zweite Faktor wird entfernt – dein Konto ist danach nur noch per Passwort geschützt.',
            fields: [{ name: 'password', label: 'Passwort zur Bestätigung', type: 'password', required: true }],
            save: async (data) => {
                await api.post('/auth/2fa/disable', { password: data.password });
                toast('2FA deaktiviert', 'success'); state.user.totp_enabled = false; render();
            },
        });
    }
    async function regenerateRecoveryCodes() {
        const data = await formModal({
            title: 'Recovery-Codes neu generieren', submitLabel: 'Generieren',
            danger: 'Alle bisherigen Recovery-Codes werden sofort ungültig.',
            fields: [{ name: 'password', label: 'Passwort zur Bestätigung', type: 'password', required: true }],
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
    // ================= STELLPLÄTZE / GARAGENPLANER =================
    // Faithful port of the Hallen-/Stellplatzverwaltung prototype: two modes in one
    // hall view — Garagenplaner (floor shape, Maße, Torhöhe, Bodenlast, ausgenommene
    // Flächen) and Stellplätze (free placement of Gefährte with dimension/collision
    // checks, drag-&-drop rebooking). Works in METRES; CSP-safe (no inline styles —
    // classes in style.css + CSSOM). Persistence: hall geometry via Save, placements
    // (spots) persist immediately on drop/remove.
    const SVGNS = 'http://www.w3.org/2000/svg';
    const svgEl = (tag, attrs = {}) => { const n = document.createElementNS(SVGNS, tag); for (const [k, v] of Object.entries(attrs)) if (v != null) n.setAttribute(k, v); return n; };
    const asObj = (v) => { if (v == null) return {}; if (typeof v === 'object') return v; try { return JSON.parse(v || '{}'); } catch (e) { return {}; } };
    const num = (v, d) => { const n = Number(v); return Number.isFinite(n) ? n : d; };

    // --- pure geometry (metres) ---
    const polyArea = (pts) => { let a = 0; for (let i = 0; i < pts.length; i++) { const j = (i + 1) % pts.length; a += pts[i][0] * pts[j][1] - pts[j][0] * pts[i][1]; } return Math.abs(a) / 2; };
    const polyPerim = (pts) => { let s = 0; for (let i = 0; i < pts.length; i++) { const a = pts[i], b = pts[(i + 1) % pts.length]; s += Math.hypot(b[0] - a[0], b[1] - a[1]); } return s; };
    const pip = (x, y, pts) => { let inside = false; for (let i = 0, j = pts.length - 1; i < pts.length; j = i++) { const xi = pts[i][0], yi = pts[i][1], xj = pts[j][0], yj = pts[j][1]; if (((yi > y) !== (yj > y)) && (x < (xj - xi) * (y - yi) / (yj - yi) + xi)) inside = !inside; } return inside; };
    const ccw = (p, q, r) => (r[1] - p[1]) * (q[0] - p[0]) > (q[1] - p[1]) * (r[0] - p[0]);
    const segX = (a, b, c, d) => ccw(a, c, d) !== ccw(b, c, d) && ccw(a, b, c) !== ccw(a, b, d);
    const rectInPoly = (r, pts) => { const cx = r.x + r.w / 2, cy = r.y + r.h / 2, e = 0.02, C = [[r.x, r.y], [r.x + r.w, r.y], [r.x + r.w, r.y + r.h], [r.x, r.y + r.h]]; const Cs = C.map(([x, y]) => [x + (cx > x ? e : -e), y + (cy > y ? e : -e)]); /* shrink toward centre for BOTH tests: a corner/edge flush on a floor border must not read as outside (pip) NOR as a T-junction crossing (segX) */ for (let k = 0; k < 4; k++) { if (!pip(Cs[k][0], Cs[k][1], pts)) return false; } for (let i = 0; i < pts.length; i++) { const p1 = pts[i], p2 = pts[(i + 1) % pts.length]; for (let c = 0; c < 4; c++) { if (segX(p1, p2, Cs[c], Cs[(c + 1) % 4])) return false; } } return true; };
    const ov = (a, b) => a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y;
    const fmtM = (v) => (Math.round(v * 100) / 100).toLocaleString('de-DE') + ' m';
    // A rotated block is inside the floor only if all corners are in the polygon AND
    // no block edge crosses a floor edge (catches spanning a concave L/U/step notch).
    const quadInPoly = (q, pts) => { const cx = (q[0][0] + q[1][0] + q[2][0] + q[3][0]) / 4, cy = (q[0][1] + q[1][1] + q[2][1] + q[3][1]) / 4, e = 0.02; const qs = q.map(([x, y]) => [x + (cx > x ? e : -e), y + (cy > y ? e : -e)]); /* shrink toward centre for BOTH the corner (pip) and edge (segX) tests so a flush corner/edge on a floor border isn't read as outside or as a T-junction crossing */ for (const [x, y] of qs) { if (!pip(x, y, pts)) return false; } for (let i = 0; i < pts.length; i++) { const p1 = pts[i], p2 = pts[(i + 1) % pts.length]; for (let c = 0; c < qs.length; c++) { if (segX(p1, p2, qs[c], qs[(c + 1) % qs.length])) return false; } } return true; };
    // Rotated-rectangle support: the 4 corners of a block rotated by rot° about its
    // centre, and a separating-axis overlap test between two convex quads.
    const quad = (b) => { const cx = b.x + b.w / 2, cy = b.y + b.h / 2, r = (b.rot || 0) * Math.PI / 180, cos = Math.cos(r), sin = Math.sin(r); return [[b.x, b.y], [b.x + b.w, b.y], [b.x + b.w, b.y + b.h], [b.x, b.y + b.h]].map(([px, py]) => { const dx = px - cx, dy = py - cy; return [cx + dx * cos - dy * sin, cy + dx * sin + dy * cos]; }); };
    const satOverlap = (A, B) => { const EPS = 0.02; for (const poly of [A, B]) { for (let i = 0; i < poly.length; i++) { const [x1, y1] = poly[i], [x2, y2] = poly[(i + 1) % poly.length]; let ax = -(y2 - y1), ay = x2 - x1; const len = Math.hypot(ax, ay) || 1; ax /= len; ay /= len; let mnA = Infinity, mxA = -Infinity, mnB = Infinity, mxB = -Infinity; for (const [px, py] of A) { const d = px * ax + py * ay; if (d < mnA) mnA = d; if (d > mxA) mxA = d; } for (const [px, py] of B) { const d = px * ax + py * ay; if (d < mnB) mnB = d; if (d > mxB) mxB = d; } if (Math.min(mxA, mxB) - Math.max(mnA, mnB) < EPS) return false; } } return true; }; // touching (≤2cm) counts as separated, so edge-flush snapping is valid

    // Hall floor shape presets (metres), parameterised by Wm×Hm.
    const gpShape = (n, W, H) => { const r = Math.round;
        if (n === 'rect') return [[0, 0], [W, 0], [W, H], [0, H]];
        if (n === 'l') return [[0, 0], [r(W * .62), 0], [r(W * .62), r(H * .5)], [W, r(H * .5)], [W, H], [0, H]];
        if (n === 'trap') return [[r(W * .16), 0], [r(W * .84), 0], [W, H], [0, H]];
        if (n === 'step') return [[0, 0], [W, 0], [W, r(H * .42)], [r(W * .6), r(H * .42)], [r(W * .6), H], [0, H]];
        if (n === 'u') return [[0, 0], [r(W * .3), 0], [r(W * .3), r(H * .6)], [r(W * .7), r(H * .6)], [r(W * .7), 0], [W, 0], [W, H], [0, H]];
        return [[0, 0], [W, 0], [W, H], [0, H]]; };

    // Exclusion / structure catalogue. Each entry is an opaque geometry block
    // {kind,x,y,w,h,rot,label(,mat)}; `cat` groups it in the rail (wall/open/zone),
    // `zone:true` marks a NON-blocking area (Stellfläche — a marking, not a barrier).
    // A wall's "Dicke" is simply its shorter side, editable via size fields/handles.
    // lane/maint/wall/exit/gate keep their original kinds for full backward compat.
    const EXCL = {
        // Wände (Mauertypen) — Dicke = kürzere Kante
        wall_ext:  { label: 'Außenwand',     w: 6,   h: 0.30,  cat: 'wall', mat: 'stahlbeton' },
        wall_load: { label: 'Tragende Wand', w: 5,   h: 0.24,  cat: 'wall', mat: 'ziegel' },
        wall_part: { label: 'Trennwand',     w: 4,   h: 0.115, cat: 'wall', mat: 'leichtbau' },
        wall_fire: { label: 'Brandwand',     w: 5,   h: 0.30,  cat: 'wall', mat: 'stahlbeton' },
        column:    { label: 'Stütze',        w: 0.4, h: 0.4,   cat: 'wall', mat: 'stahlbeton' },
        wall:      { label: 'Säule/Wand',    w: 1,   h: 1,     cat: 'wall' }, // Legacy
        // Öffnungen
        gate:      { label: 'Tor',     w: 3,   h: 0.5, cat: 'open' },
        door:      { label: 'Tür',     w: 1,   h: 0.3, cat: 'open' },
        window:    { label: 'Fenster', w: 1.4, h: 0.3, cat: 'open' },
        // Flächen & Zonen
        stell:     { label: 'Stellfläche', w: 5, h: 2.5, cat: 'zone', zone: true }, // markiert, nicht sperrend
        lane:      { label: 'Fahrstraße',  w: 6, h: 2,   cat: 'zone' },
        maint:     { label: 'Wartung',     w: 4, h: 3,   cat: 'zone' },
        exit:      { label: 'Notausgang',  w: 2, h: 1,   cat: 'zone' },
    };
    // Wall/column material labels (Mauertyp-Material) — display only.
    const MAT = { stahlbeton: 'Stahlbeton', ziegel: 'Ziegel', leichtbau: 'Leichtbau', kalksand: 'Kalksandstein', holz: 'Holz' };
    // Richtwert Muster-Garagenverordnung (je Bundesland abweichend): Stellplatz ≥ 2,30 × 5,00 m.
    // Nur weiche Warnung an der Stellfläche, nie sperrend (S4-A). Die Fahrgassen-6-m-Regel ist zu
    // kontextabhängig (Aufstellwinkel/Ein-/Zweirichtung), um sie ohne Fehlalarme zu prüfen.
    const GAVO = { stallW: 2.30, stallL: 5.00 };
    const zoneSizeWarn = (b) => {
        if (b.kind !== 'stell') return null;
        const lo = Math.min(b.w, b.h), hi = Math.max(b.w, b.h);
        return (lo < GAVO.stallW - 0.005 || hi < GAVO.stallL - 0.005) ? '⚠ < Mindestmaß' : null;
    };
    const isZoneKind = (k) => !!(EXCL[k] && EXCL[k].zone);      // non-blocking marked area
    const isWallKind = (k) => !!(EXCL[k] && EXCL[k].cat === 'wall');
    const isWallDraw = (k) => !!(k && String(k).indexOf('wall_') === 0); // drawable wall segment (not column / legacy wall)
    const isOpenKind = (k) => !!(EXCL[k] && EXCL[k].cat === 'open');
    // Wall node-graph: the building is drawn as connected wall segments (edges) between
    // shared points (nodes). Openings (Tür/Fenster/Tor) live ON an edge as {c,w,kind}.
    // This is the primary structure model; excl rectangles remain for functional zones
    // (Fahrstraße/Wartung/Notausgang/Stellfläche) and legacy data.
    const wallKindsList = () => Object.keys(EXCL).filter((k) => EXCL[k].cat === 'wall' && k !== 'column' && k !== 'wall');
    const openKindsList = () => Object.keys(EXCL).filter((k) => EXCL[k].cat === 'open');
    const isZoneTool = (k) => !!(EXCL[k] && EXCL[k].cat === 'zone'); // lane/maint/exit/stell → rubber-band draw
    const WALLCOL = { wall_ext: '#8aa0b6', wall_load: '#b08968', wall_part: '#9fb2c4', wall_fire: '#d06a63' };
    const OPENCOL = { gate: '#4ea8f5', door: '#5adcb4', window: '#7ec8ff' };
    const ZONECOL = { lane: '#e0a94a', maint: '#e0b452', exit: '#3ddc97', stell: '#2bb47a' };
    const normalizeWalls = (w) => {
        const nodes = Array.isArray(w && w.nodes) ? w.nodes.map((n) => ({ x: num(n.x, 0), y: num(n.y, 0) })) : [];
        const okWall = (k) => EXCL[k] && EXCL[k].cat === 'wall' && k !== 'column' && k !== 'wall';
        const edges = (Array.isArray(w && w.edges) ? w.edges : []).filter((e) => Number.isInteger(e.a) && Number.isInteger(e.b) && e.a !== e.b && nodes[e.a] && nodes[e.b]).map((e) => ({
            a: e.a, b: e.b,
            kind: okWall(e.kind) ? e.kind : 'wall_ext',
            thick: num(e.thick, (EXCL[e.kind] && EXCL[e.kind].h) || 0.24),
            ops: (Array.isArray(e.ops) ? e.ops : []).map((o) => { const op = { c: Math.max(0, Math.min(1, num(o.c, 0.5))), w: Math.max(0.1, num(o.w, 1)), kind: isOpenKind(o.kind) ? o.kind : 'door' };
                if (o.side === -1) op.side = -1; if (o.hinge === 1) op.hinge = 1; if (num(o.frame, 0) > 0) op.frame = Math.max(0, num(o.frame, 0)); return op; }), // keep Türanschlag (side/hinge) + frame across reloads
        }));
        return { nodes, edges };
    };
    // Status carries a SYMBOL + label so it is distinguishable without colour (a11y).
    const GPSTAT = { busy: { label: 'Belegt', sym: '●' }, resv: { label: 'Reserviert', sym: '◑' }, move: { label: 'Ein/Aus', sym: '⇄' } };

    // Fahrzeug-Darstellung: Kategoriename (v.type) → Top-View-Symbol-Key + Standard-Footprint.
    const catKey = (type) => { const t = (type || '').toLowerCase();
        if (/motorr|moped|roller|bike/.test(t)) return 'Motorrad';
        if (/wohnmobil|reisemobil|camper/.test(t)) return 'Wohnmobil';
        if (/wohnwagen|caravan/.test(t)) return 'Wohnwagen';
        if (/boot|jetski|trailer/.test(t)) return 'Boot / Trailer';
        if (/traktor|schlepper|landmasch/.test(t)) return 'Traktor';
        if (/ladewagen/.test(t)) return 'Ladewagen';
        if (/rückewagen|rueckewagen|forst/.test(t)) return 'Rückewagen';
        if (/kipper|mulde/.test(t)) return 'Kipper';
        if (/anhäng|anhaeng|häng|haeng/.test(t)) return 'Anhänger';
        if (/transport|sprinter|kasten|\bvan\b|bus/.test(t)) return 'Transporter';
        return 'PKW'; };
    const CATDEF = { PKW: [4.5, 1.9], Motorrad: [2.2, 0.9], Transporter: [5.4, 2.1], Wohnmobil: [7.0, 2.3], Wohnwagen: [6.0, 2.2], 'Anhänger': [4.0, 1.8], 'Boot / Trailer': [6.0, 2.3], Traktor: [4.6, 2.2], Ladewagen: [8.0, 2.5], 'Rückewagen': [7.5, 2.4], Kipper: [6.5, 2.4] };
    const catFoot = (type) => CATDEF[catKey(type)] || [4.5, 2];
    // Built-in top-view icons: category → static raster asset (drawn VERTICAL, front up).
    // Rendered inside the same rotate(-90°) group as the drawn fallback so they lie along
    // the landscape block. A category with no asset falls back to catSymbol (drawn SVG).
    const CATICON = { PKW: 'pkw', Motorrad: 'motorrad', Transporter: 'transporter', Wohnmobil: 'wohnmobil', Wohnwagen: 'wohnwagen', 'Anhänger': 'anhaenger', 'Boot / Trailer': 'boot', Traktor: 'traktor', Ladewagen: 'ladewagen', 'Rückewagen': 'rueckewagen', Kipper: 'kipper' };
    const CATICON_BASE = '/img/vehicles/';

    // Top-view category icon (detailed, two-tone) as inner SVG markup in a fixed 62 x 100
    // box, drawn VERTICAL (front = top): white body, dark rounded outline, green accents.
    function catSymbol(cat) {
        var INK = '#333b45', BODY = '#fbfcfd', GRN = '#9cc188', TIRE = '#4a5563';
        function rr(x, y, w, h, r, f) { return '<rect x="' + x + '" y="' + y + '" width="' + w + '" height="' + h + '" rx="' + r + '" fill="' + (f || BODY) + '" stroke="' + INK + '" stroke-width="2.2" stroke-linejoin="round"/>'; }
        function gf(x, y, w, h, r) { return '<rect x="' + x + '" y="' + y + '" width="' + w + '" height="' + h + '" rx="' + (r == null ? 2 : r) + '" fill="' + GRN + '"/>'; }
        function tire(x, y, w, h) { return '<rect x="' + x + '" y="' + y + '" width="' + w + '" height="' + h + '" rx="' + (Math.min(w, h) / 2.2) + '" fill="' + TIRE + '" stroke="' + INK + '" stroke-width="1.4"/>'; }
        function gw(x, y, w, h) { return '<rect x="' + x + '" y="' + y + '" width="' + w + '" height="' + h + '" rx="' + (Math.min(w, h) / 2.4) + '" fill="' + GRN + '" stroke="' + INK + '" stroke-width="1.6"/>'; }
        function ln(a, b, c, d) { return '<line x1="' + a + '" y1="' + b + '" x2="' + c + '" y2="' + d + '" stroke="' + INK + '" stroke-width="1.4" stroke-linecap="round"/>'; }
        function ci(x, y, r, f) { return '<circle cx="' + x + '" cy="' + y + '" r="' + r + '" fill="' + (f || BODY) + '" stroke="' + INK + '" stroke-width="2"/>'; }
        function dot(x, y, r, f) { return '<circle cx="' + x + '" cy="' + y + '" r="' + r + '" fill="' + (f || GRN) + '"/>'; }
        function P(d, f) { return '<path d="' + d + '" fill="' + (f || BODY) + '" stroke="' + INK + '" stroke-width="2.2" stroke-linejoin="round"/>'; }
        function bar() { return ln(31, 3, 31, 14) + ci(31, 4, 2); }
        var S = '<g stroke-linecap="round">';
        switch (cat) {
            case 'PKW': case 'Auto':
                S += tire(6, 22, 7, 16) + tire(49, 22, 7, 16) + tire(6, 60, 7, 16) + tire(49, 60, 7, 16)
                    + rr(11, 6, 40, 88, 10) + gf(9, 25, 4, 6, 2) + gf(49, 25, 4, 6, 2)
                    + P('M23,15 L39,15 L45,32 L17,32 Z', GRN)                        // windshield (big, narrow at front)
                    + gf(14, 36, 6, 26, 2) + gf(42, 36, 6, 26, 2)                    // side windows
                    + P('M24,71 L38,71 Q42,77 39,83 L23,83 Q20,77 24,71 Z', GRN)     // rear window (small, rounded)
                    + dot(19, 10, 2) + dot(43, 10, 2); break;
            case 'Motorrad':
                S += P('M31,12 C21,13 17,24 17,44 C17,64 22,82 31,96 C40,82 45,64 45,44 C45,24 41,13 31,12 Z')
                    + '<path d="M15,19 Q31,13 47,19" fill="none" stroke="' + INK + '" stroke-width="2.6" stroke-linecap="round"/>'
                    + dot(14, 19, 2.4) + dot(48, 19, 2.4)
                    + ci(31, 27, 5.5) + dot(31, 27, 2.2) + gf(24, 44, 14, 32, 7); break;
            case 'Transporter':
                S += tire(4, 20, 7, 15) + tire(51, 20, 7, 15) + tire(4, 66, 7, 15) + tire(51, 66, 7, 15)
                    + rr(11, 6, 40, 88, 8) + P('M15,11 L47,11 L44,23 L18,23 Z', GRN)
                    + ln(11, 29, 51, 29) + gf(7, 19, 5, 8, 2) + gf(50, 19, 5, 8, 2)
                    + rr(15, 34, 32, 56, 3) + ln(31, 34, 31, 90); break;
            case 'Wohnmobil':
                S += tire(4, 15, 7, 15) + tire(51, 15, 7, 15) + tire(4, 44, 7, 15) + tire(51, 44, 7, 15) + tire(4, 73, 7, 15) + tire(51, 73, 7, 15)
                    + rr(11, 5, 40, 90, 7) + P('M15,10 L47,10 L44,21 L18,21 Z', GRN) + ln(11, 26, 51, 26)
                    + gf(8, 14, 4, 8, 2) + gf(50, 14, 4, 8, 2)
                    + gf(22, 34, 18, 14, 3) + rr(24, 54, 14, 12, 2) + ln(15, 72, 47, 72) + ln(15, 80, 47, 80) + gf(45, 58, 6, 18, 2); break;
            case 'Wohnwagen':
                S += bar() + tire(6, 52, 7, 16) + tire(49, 52, 7, 16)
                    + P('M31,14 C18,14 13,20 13,30 L13,86 C13,92 16,95 22,95 L40,95 C46,95 49,92 49,86 L49,30 C49,20 44,14 31,14 Z')
                    + gf(15, 40, 8, 44, 3) + gf(39, 40, 8, 44, 3) + rr(22, 22, 18, 11, 3) + gf(24, 24, 14, 7, 2) + rr(27, 58, 8, 8, 2); break;
            case 'Anhänger':
                S += bar() + ln(31, 14, 18, 30) + ln(31, 14, 44, 30)
                    + tire(5, 46, 7, 16) + tire(50, 46, 7, 16)
                    + rr(12, 30, 38, 52, 3) + gf(12, 30, 5, 52, 2) + gf(45, 30, 5, 52, 2)
                    + ln(18, 46, 44, 46) + ln(18, 64, 44, 64); break;
            case 'Boot / Trailer': case 'Boot':
                S += tire(6, 58, 7, 16) + tire(49, 58, 7, 16)
                    + P('M31,4 C21,12 16,28 16,52 L16,84 C16,90 20,94 26,94 L36,94 C42,94 46,90 46,84 L46,52 C46,28 41,12 31,4 Z')
                    + gf(23, 52, 16, 26, 5) + rr(24, 40, 14, 9, 3) + rr(26, 88, 10, 7, 2); break;
            case 'Traktor': case 'Landmaschine':
                S += tire(9, 8, 10, 20) + tire(43, 8, 10, 20) + gw(3, 52, 15, 38) + gw(44, 52, 15, 38)
                    + ln(6, 60, 15, 60) + ln(6, 70, 15, 70) + ln(6, 80, 15, 80) + ln(47, 60, 56, 60) + ln(47, 70, 56, 70) + ln(47, 80, 56, 80)
                    + rr(22, 8, 18, 26, 4) + rr(18, 34, 26, 34, 4) + gf(21, 38, 20, 26, 3) + rr(28, 2, 6, 8, 1); break;
            case 'Ladewagen':
                S += bar() + tire(4, 42, 7, 15) + tire(4, 62, 7, 15) + tire(51, 42, 7, 15) + tire(51, 62, 7, 15)
                    + rr(11, 22, 40, 70, 3) + gf(11, 22, 8, 70, 2) + gf(43, 22, 8, 70, 2)
                    + ln(22, 26, 22, 88) + ln(26, 26, 26, 88) + ln(30, 26, 30, 88) + ln(34, 26, 34, 88) + ln(38, 26, 38, 88)
                    + P('M19,14 L43,14 L47,22 L15,22 Z'); break;
            case 'Rückewagen':
                S += tire(4, 48, 7, 15) + tire(4, 68, 7, 15) + tire(51, 48, 7, 15) + tire(51, 68, 7, 15)
                    + rr(11, 32, 40, 60, 3)
                    + ln(11, 42, 6, 42) + ln(11, 58, 6, 58) + ln(11, 74, 6, 74) + ln(11, 88, 6, 88)
                    + ln(51, 42, 56, 42) + ln(51, 58, 56, 58) + ln(51, 74, 56, 74) + ln(51, 88, 56, 88)
                    + gf(18, 38, 7, 52, 3) + gf(28, 38, 7, 52, 3) + gf(38, 38, 7, 52, 3)     // logs
                    + gf(23, 20, 16, 11, 3) + ln(31, 20, 31, 7) + ln(31, 7, 46, 12) + ln(46, 12, 46, 23) + P('M43,23 L49,23 L46,29 Z', GRN); break; // crane + hook
            case 'Kipper':
                S += bar() + ln(31, 14, 20, 24) + ln(31, 14, 42, 24)
                    + tire(4, 44, 7, 15) + tire(4, 64, 7, 15) + tire(51, 44, 7, 15) + tire(51, 64, 7, 15)
                    + rr(12, 24, 38, 66, 3) + rr(17, 29, 28, 50, 2) + gf(12, 80, 38, 10, 3); break;
            default:
                S += tire(6, 24, 7, 15) + tire(49, 24, 7, 15) + tire(6, 60, 7, 15) + tire(49, 60, 7, 15)
                    + rr(13, 6, 36, 88, 16) + gf(19, 16, 24, 14, 4) + gf(19, 70, 24, 14, 4);
        }
        return S + '</g>';
    }
    // Cached SVG symbol node keyed by category; cloned per block so a plan with 60-100
    // vehicles parses each shape once, not once per draw. The icon is drawn vertical
    // (62x100) and rotated -90° into a 100x62 box so it lies along the (landscape) block.
    const symCache = {};
    function symbolNode(type) {
        const cat = catKey(type);
        let tpl = symCache[cat];
        if (!tpl) {
            tpl = svgEl('svg', { class: 'gp-sym', viewBox: '0 0 100 62', preserveAspectRatio: 'xMidYMid meet' });
            const file = CATICON[cat];
            if (file) {
                // Raster asset placed in the 62x100 (pre-rotation) space, then rotated into
                // the 100x62 landscape box just like the drawn symbol it replaces.
                const g = svgEl('g', { transform: 'translate(0,62) rotate(-90)' });
                const im = svgEl('image', { x: 0, y: 0, width: 62, height: 100, preserveAspectRatio: 'xMidYMid meet' });
                im.setAttribute('href', CATICON_BASE + file + '.png');
                g.append(im); tpl.append(g);
            } else {
                tpl.innerHTML = '<g transform="translate(0,62) rotate(-90)">' + catSymbol(cat) + '</g>';
            }
            symCache[cat] = tpl;
        }
        return tpl.cloneNode(true);
    }

    // ---- garages list ----
    routes.garages = async (page) => {
        const garages = await api.get('/garages');
        page.innerHTML = '';
        const head = el('div', { class: 'page-head' }, el('h2', {}, 'Stellplätze'));
        if (canManage()) head.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => garageForm() }, '+ Garage'));
        page.append(head);
        if (!garages.length) { page.append(emptyState('box', 'Noch keine Garage angelegt.')); return; }
        const list = el('div', {});
        garages.forEach((g) => list.append(el('div', { class: 'card' },
            el('div', { class: 'card-row' },
                el('div', { style: 'flex:1;cursor:pointer', onclick: () => navigate('garage/' + g.id) },
                    el('h3', {}, g.name),
                    el('div', { class: 'card-meta' }, (g.hall_count || 0) + (g.hall_count === 1 ? ' Halle' : ' Hallen'))),
                el('div', { class: 'card-actions' },
                    el('button', { class: 'btn btn-ghost btn-sm', 'aria-label': g.name + ' öffnen', onclick: () => navigate('garage/' + g.id) }, '›'),
                    canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: 'Umbenennen', onclick: () => garageForm(g) }, icon('edit')),
                    canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: 'Löschen', onclick: () => delGarage(g) }, icon('trash')))))));
        page.append(list);
    };
    async function garageForm(g) {
        await formModal({ title: g ? 'Garage umbenennen' : 'Neue Garage', submitLabel: 'Speichern',
            fields: [{ name: 'name', label: 'Name', required: true, value: g ? g.name : '' }],
            save: async (d) => { if (g) await api.put('/garages/' + g.id, { name: d.name.trim() }); else await api.post('/garages', { name: d.name.trim() }); } });
        render();
    }
    async function delGarage(g) {
        if (!await confirmDialog('Garage löschen?', `„${g.name}" samt allen Hallen und Stellplätzen wird entfernt. Zugewiesene Gefährte werden freigegeben (nicht gelöscht).`, 'Löschen')) return;
        await api.del('/garages/' + g.id); toast('Garage gelöscht'); render();
    }

    // ---- halls of a garage ----
    routes.garage = async (page, id) => {
        const [garages, halls] = await Promise.all([api.get('/garages'), api.get('/garages/' + id + '/halls')]);
        const g = garages.find((x) => x.id === id) || { name: 'Garage' };
        page.innerHTML = '';
        page.append(el('div', { class: 'detail-head' },
            el('button', { class: 'back-btn', 'aria-label': 'Zurück', onclick: () => navigate('garages') }, '‹'),
            el('h2', {}, g.name)));
        const head = el('div', { class: 'page-head' }, el('h3', {}, 'Hallen'));
        if (canManage()) head.append(el('button', { class: 'btn btn-primary btn-sm', onclick: () => hallForm(id) }, '+ Halle'));
        page.append(head);
        if (!halls.length) { page.append(emptyState('box', 'Noch keine Halle in dieser Garage.')); return; }
        const list = el('div', {});
        halls.forEach((hl) => {
            const geo = asObj(hl.geometry); const area = (geo.floor && geo.floor.length >= 3) ? polyArea(geo.floor) : 0;
            list.append(el('div', { class: 'card' },
                el('div', { class: 'card-row' },
                    el('div', { style: 'flex:1;cursor:pointer', onclick: () => navigate('hall/' + hl.id) },
                        el('h3', {}, hl.name),
                        el('div', { class: 'card-meta' }, (hl.spot_count || 0) + ' Gefährte' + (area ? ' · ' + Math.round(area).toLocaleString('de-DE') + ' m²' : ''))),
                    el('div', { class: 'card-actions' },
                        el('button', { class: 'btn btn-ghost btn-sm', onclick: () => navigate('hall/' + hl.id) }, 'Planer ›'),
                        canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: 'Umbenennen', onclick: () => hallForm(id, hl) }, icon('edit')),
                        canManage() && el('button', { class: 'btn btn-ghost btn-sm', title: 'Löschen', onclick: () => delHall(hl) }, icon('trash'))))));
        });
        page.append(list);
    };
    async function hallForm(garageID, hl) {
        await formModal({ title: hl ? 'Halle umbenennen' : 'Neue Halle', submitLabel: 'Speichern',
            fields: [{ name: 'name', label: 'Name', required: true, value: hl ? hl.name : '' }],
            save: async (d) => {
                if (hl) await api.put('/halls/' + hl.id, { name: d.name.trim(), geometry: asObj(hl.geometry) });
                else await api.post('/garages/' + garageID + '/halls', { name: d.name.trim(), geometry: { floor: gpShape('rect', 14, 9), Wm: 14, Hm: 9, tor: 3, load: 5, shape: 'rect', excl: [] } });
            } });
        render();
    }
    async function delHall(hl) {
        if (!await confirmDialog('Halle löschen?', `„${hl.name}" samt Stellplätzen wird entfernt. Zugewiesene Gefährte werden freigegeben.`, 'Löschen')) return;
        await api.del('/halls/' + hl.id); toast('Halle gelöscht'); render();
    }

    // ---- the planner ----
    routes.hall = async (page, id) => {
        const data = await api.get('/halls/' + id + '/plan');
        const [palette, halls, plannerIcons] = await Promise.all([api.get('/vehicles/unassigned'), api.get('/garages/' + data.hall.garage_id + '/halls'), api.get('/planner-icons').catch(() => [])]);
        const geo = asObj(data.hall.geometry);
        const Wm = num(geo.Wm, 14), Hm = num(geo.Hm, 9);
        let floor = Array.isArray(geo.floor) && geo.floor.length >= 3 ? geo.floor.map((p) => [num(p[0], 0), num(p[1], 0)]) : gpShape(geo.shape || 'rect', Wm, Hm);
        const P = {
            hallId: id, garageId: data.hall.garage_id, hallName: data.hall.name, garageName: data.garage_name, halls,
            floor, Wm, Hm, tor: num(geo.tor, 3), load: num(geo.load, 5), shape: geo.shape || 'rect',
            excl: (Array.isArray(geo.excl) ? geo.excl : []).map((e, i) => ({ id: 'x' + i, kind: e.kind || 'wall', x: num(e.x, 0), y: num(e.y, 0), w: num(e.w, 1), h: num(e.h, 1), rot: num(e.rot, 0), label: e.label || (EXCL[e.kind] ? EXCL[e.kind].label : 'Fläche'), mat: e.mat || (EXCL[e.kind] ? EXCL[e.kind].mat : undefined) })),
            // Optional Bauplan underlay: a downscaled image (data-URL) placed in metre space.
            plan: (geo.plan && geo.plan.href) ? { href: geo.plan.href, x: num(geo.plan.x, 0), y: num(geo.plan.y, 0), w: num(geo.plan.w, Wm), h: num(geo.plan.h, Hm), opacity: num(geo.plan.opacity, 0.55), hidden: !!geo.plan.hidden } : null,
            // Wall node-graph (primary building structure — drawn interactively).
            walls: normalizeWalls(geo.walls),
            spots: (data.spots || []).map((s) => { const g = asObj(s.geometry); return {
                _id: s.id, kind: 'veh', label: s.vehicle_label || s.label, spotLabel: s.label, type: s.vehicle_type || '', vehId: s.vehicle_id || null,
                personId: s.person_id || null, personName: s.person_name || '',
                L: s.length_m, W: s.width_m, H: s.height_m, t: s.weight_t, needsPower: !!s.needs_power,
                plannerSymbol: s.planner_symbol || null, photoUrl: s.photo_id ? '/api/photos/' + s.photo_id : null,
                x: num(g.x, 1), y: num(g.y, 1), w: num(g.w, s.length_m || catFoot(s.vehicle_type)[0]), h: num(g.h, s.width_m || catFoot(s.vehicle_type)[1]), rot: num(g.rot, 0), status: g.status || 'busy', noBuf: !!g.noBuf, _dirty: false }; }),
            palette, plannerIcons,
            mode: 'manage', snap: true, gridStep: 0.5, autoSnap: true, calib: false, autoArea: true, ortho: false, zoom: 1, base: null, buffer: false, bufferM: 1, roomSel: null, wallRef: (geo.wallRef === 'inner' || geo.wallRef === 'outer') ? geo.wallRef : 'axis', sel: null,
            tool: null, chain: null, preview: null, structSel: null, // wall draw/edit state
            openStart: null, snapHint: null, drawKind: 'wall_ext', openKind: 'door', wallThick: null, // opening draw + last-used types + wall-thickness override (m)
            render: (function () { try { return localStorage.getItem('gp.render') || 'symbol'; } catch (e) { return 'symbol'; } })(),
            maxed: false, CELL: 30, uid: 1, hist: [], hpos: -1, dirty: false,
        };
        buildGP(page, P);
    };

    function buildGP(page, P) {
        page.innerHTML = ''; // clear the route skeleton (else it leaves empty cards above)
        const canManageNow = canManage();
        // ---- validity helpers bound to this hall ----
        const inside = (b) => (b.rot ? quadInPoly(quad(b), P.floor) : rectInPoly(b, P.floor));
        const heightOK = (b) => b.H == null || b.H <= P.tor + 0.001;
        const weightOK = (b) => b.t == null || b.t <= P.load + 0.001;
        const warn = (b) => b.kind === 'veh' && (!heightOK(b) || !weightOK(b) || !inside(b));
        // Zones (Stellfläche) are markings, not barriers → kept out of the collision set.
        // Wall segments (graph) ARE barriers: vehicles can't overlap them.
        const blocksAll = () => P.spots.concat(P.excl.filter((e) => !isZoneKind(e.kind)).map((e) => ({ ...e, kind: 'excl' }))).concat(wallRects().map((r) => ({ ...r, kind: 'excl' })));
        const expandQ = (b, m) => quad({ x: b.x - m, y: b.y - m, w: b.w + 2 * m, h: b.h + 2 * m, rot: b.rot });
        // Collision. When buffer zones are on, ONLY vehicle↔vehicle pairs get the clearance
        // (each half of P.bufferM) — walls, lanes and maintenance keep exact (edge-to-edge)
        // footprints, and a vehicle with noBuf opts out entirely.
        const collide = (cand, selfId) => {
            const bm = P.buffer ? P.bufferM / 2 : 0;
            const candNoBuf = cand.noBuf || (selfId != null && (P.spots.find((s) => s._id === selfId) || {}).noBuf);
            const cq = quad(cand);
            for (const o of blocksAll()) { if (o._id === selfId || o.id === selfId) continue; if (!(cand.kind === 'veh' || o.kind === 'veh')) continue;
                const vv = bm > 0 && cand.kind === 'veh' && o.kind === 'veh' && !candNoBuf && !o.noBuf;
                if (satOverlap(vv ? expandQ(cand, bm) : cq, vv ? expandQ(o, bm) : quad(o))) return true;
            }
            return false;
        };
        const validVeh = (cand, selfId) => inside(cand) && !collide(cand, selfId);
        // Door leaf sweep as a conservative quad (the w×w square on the hinge's swing side), so an
        // aufschlagende Tür can be flagged (soft warning) where it overlaps a vehicle/Stellfläche (S3-A).
        function doorSweepQuad(e, o) {
            const v = edgeVec(e); if (v.len < 0.02) return null; const hp = (o.w / v.len) / 2, w = o.w, ei = P.walls.edges.indexOf(e);
            const A = offCenterPt(ei, o.c - hp), B = offCenterPt(ei, o.c + hp); // sweep on the mitre-offset centre line where the door renders (F5/C3)
            const nx = A.nx, ny = A.ny, s = o.side || 1, hinge = o.hinge ? B : A, jamb = o.hinge ? A : B;
            return [[hinge.x, hinge.y], [jamb.x, jamb.y], [jamb.x + nx * s * w, jamb.y + ny * s * w], [hinge.x + nx * s * w, hinge.y + ny * s * w]];
        }
        const sweepHitsSpot = (sw) => { if (!sw) return false; for (const b of P.spots) if (satOverlap(sw, quad(b))) return true; return false; };
        const doorHitsSpot = (b) => { const bq = quad(b); for (const e of P.walls.edges) for (const o of (e.ops || [])) { if (o.kind !== 'door') continue; const sw = doorSweepQuad(e, o); if (sw && satOverlap(sw, bq)) return true; } return false; };
        // Rohbau- vs. lichtes Maß: o.w is the structural wall opening; the clear passage deducts the
        // frame/reveal per side (o.frame, default 6 cm for doors, 0 for gates/windows) (S3-B).
        const frameOf = (o) => (o.frame != null ? o.frame : (o.kind === 'door' ? 0.06 : 0));
        const clearWidth = (o) => Math.max(0, o.w - 2 * frameOf(o));
        // A vehicle placement has kind==='veh'; every exclusion has kind in EXCL
        // (lane/maint/wall/exit). In Garagenplaner the exclusions are editable, in
        // Stellplätze the vehicles are.
        const interactive = (b) => (P.mode === 'plan' ? b.kind !== 'veh' : b.kind === 'veh');
        const dimText = (b) => (b.type ? b.type + ' · ' : '') + (b.L != null && b.W != null ? b.L.toFixed(1).replace('.', ',') + '×' + b.W.toFixed(1).replace('.', ',') + ' m' : 'Maße offen');

        // ---- shell DOM ----
        const root = el('div', { class: 'gp' }); root.dataset.mode = P.mode;
        page.append(root);
        // appbar
        const modeSwitch = el('div', { class: 'gp-switch' });
        const mkModeBtn = (m, label) => el('button', { 'data-mode': m, class: P.mode === m ? 'on' : '', onclick: () => setMode(m) }, label);
        const rebuildSwitch = () => { modeSwitch.innerHTML = ''; modeSwitch.append(mkModeBtn('plan', 'Garagenplaner'), mkModeBtn('manage', 'Stellplätze')); };
        const occN = el('b', { class: 'num' }, '–'); const occBar = el('i', {});
        const maxBtn = el('button', { class: 'gp-iconbtn', title: 'Vollbild', 'aria-label': 'Vollbild', onclick: () => toggleMax() }, '⛶');
        const appbar = el('div', { class: 'gp-appbar' },
            el('button', { class: 'back-btn', 'aria-label': 'Zurück', onclick: () => navigate(P.garageId ? 'garage/' + P.garageId : 'garages') }, '‹'),
            el('div', { class: 'gp-brand' }, el('b', {}, P.hallName), el('span', { class: 'muted' }, P.garageName)),
            modeSwitch, el('span', { class: 'gp-spacer' }),
            el('div', { class: 'gp-occ' }, el('span', { class: 'eyebrow' }, 'Belegung'), occN, el('span', { class: 'gp-bar' }, occBar)),
            maxBtn);
        rebuildSwitch();

        // canvas
        const ctitle = el('span', { class: 'gp-ctitle' }, 'Digitaler Zwilling');
        const toolbar = el('div', { class: 'gp-toolbar' });
        const metrics = el('div', { class: 'gp-metrics' });
        const svg = svgEl('svg', { class: 'gp-floor', xmlns: SVGNS });
        const layer = el('div', { class: 'gp-layer' });
        const planEl = el('div', { class: 'gp-plan' }, svg, layer);
        const planWrap = el('div', { class: 'gp-planwrap' }, planEl);
        const canvas = el('div', { class: 'gp-canvas card' },
            el('div', { class: 'gp-canvas-top' }, ctitle, el('span', { class: 'gp-spacer' }), toolbar), metrics, planWrap);

        // rail
        const rail = el('div', { class: 'gp-rail' });
        const stage = el('div', { class: 'gp-stage' }, canvas, rail);
        root.append(appbar, stage);

        // Transparent capture layer for interactive wall drawing / node editing (plan mode).
        const drawOverlay = el('div', { class: 'gp-drawoverlay' }); drawOverlay.style.display = 'none'; planEl.append(drawOverlay);
        let nodeDrag = null, opDrag = null, zoneDrag = null, zoneDraw = null;
        // FE4 — multi-select of the CURRENT subsystem's objects only: zones in the Garagenmanager
        // (plan), vehicles in the Stellplatzsystem (manage) — mirroring interactive(b). selSet holds
        // their ids; a marquee drag on empty floor fills it; group move/delete act on it.
        let selSet = [], marq = null;
        const findBlock = (id) => P.spots.find((x) => x._id == id) || P.excl.find((x) => x.id === id);
        const inSel = (id) => id != null && selSet.indexOf(id) >= 0;
        const clearSelSet = () => { if (selSet.length) { selSet = []; return true; } return false; };
        // point (metres) inside a possibly-rotated block?
        function pointInBlock(b, p) { const cx = b.x + b.w / 2, cy = b.y + b.h / 2, r = -(b.rot || 0) * Math.PI / 180, cos = Math.cos(r), sin = Math.sin(r); const dx = p.x - cx, dy = p.y - cy, lx = dx * cos - dy * sin, ly = dx * sin + dy * cos; return Math.abs(lx) <= b.w / 2 && Math.abs(ly) <= b.h / 2; }
        function createZone(kind, x, y, w, h) { const b = { id: 'e' + (P.uid++), kind, x: round2(x), y: round2(y), w: round2(w), h: round2(h), rot: 0, label: EXCL[kind].label, mat: EXCL[kind].mat }; P.excl.push(b); return b; }
        // Snap a zone/area's edges flush to the nearest axis-aligned wall FACE (inner surface).
        function snapZoneFaces(b) {
            if (!P.autoSnap) return; const SNAP = (P.snap ? (P.gridStep || 0.5) : 0.25) + 0.05, xs = [], ys = [];
            // edgePts = the Bezug-offset centreline as rendered, so faces = real inner/outer surface
            // for Innen/Außen too (raw node line ± t/2 was only correct for Achse).
            P.walls.edges.forEach((e, ei) => { const vp = edgePts(e, ei); if (vp.len < 0.02) return; const t2 = (e.thick || 0.24) / 2;
                if (Math.abs(vp.dy) < 0.02) { const cy = (vp.a.y + vp.b.y) / 2; ys.push(cy - t2, cy + t2); } else if (Math.abs(vp.dx) < 0.02) { const cx = (vp.a.x + vp.b.x) / 2; xs.push(cx - t2, cx + t2); } });
            const snap1 = (lo, hi, coords) => { let best = 0, bd = SNAP + 1e-9; for (const c of coords) { const dl = Math.abs(lo - c); if (dl < bd) { bd = dl; best = c - lo; } const dh = Math.abs(hi - c); if (dh < bd) { bd = dh; best = c - hi; } } return bd <= SNAP ? best : 0; };
            b.x += snap1(b.x, b.x + b.w, xs); b.y += snap1(b.y, b.y + b.h, ys);
        }
        // Cancel any in-progress drawing and drop back to the selection cursor (never deletes).
        function cancelDrawing() { const had = P.chain; P.tool = null; P.chain = null; P.openStart = null; P.preview = null; P.chainAnchor = null; P.snapHint = null; P.guide = null; P.attach = null; zoneDraw = null; if (had) pruneNodes(); refreshFloorFromWalls(); fitView(); }
        // Inline quick-edit popover for a selected wall segment OR opening: length/width + delete.
        const wallPopLabel = el('span', { class: 'gp-wallpop-lab' }, 'Länge');
        const wallLenIn = el('input', { type: 'number', step: '0.01', min: '0.1', class: 'gp-lenin' });
        const wallPopUnit = el('span', { class: 'muted' }, 'm');
        const wallThLabel = el('span', { class: 'gp-wallpop-lab' }, 'Dicke');
        const wallThIn = el('input', { type: 'number', step: '1', min: '5', class: 'gp-lenin' });
        const wallThUnit = el('span', { class: 'muted' }, 'cm');
        const wallDoorFlip = el('button', { class: 'gp-wallpop-btn', title: 'Öffnungsrichtung spiegeln (F)' }, '⇅');
        const wallDoorHinge = el('button', { class: 'gp-wallpop-btn', title: 'Anschlag links/rechts (Leertaste)' }, '⇄');
        const wallZoneRot = el('button', { class: 'gp-wallpop-btn', title: 'Fläche 90° drehen (R) · Knopf oben = frei drehen' }, '⟳');
        const wallPopDel = el('button', { class: 'gp-wallpop-del', title: 'Löschen', 'aria-label': 'Löschen' }, '🗑');
        const wallPop = el('div', { class: 'gp-wallpop' }, wallPopLabel, wallLenIn, wallPopUnit, wallThLabel, wallThIn, wallThUnit, wallDoorFlip, wallDoorHinge, wallZoneRot, wallPopDel);
        wallZoneRot.addEventListener('click', (e) => { e.stopPropagation(); if (P.sel != null) { const b = P.excl.find((x) => x.id === P.sel); if (b) { b.rot = ((b.rot || 0) + 90) % 360; commitGeom('Fläche gedreht'); } } });
        wallThIn.addEventListener('keydown', (e) => { e.stopPropagation(); if (e.key === 'Enter') wallThIn.blur(); });
        wallThIn.addEventListener('change', () => { if (P.structSel && P.structSel.type === 'edge') { const cm = parseFloat(wallThIn.value); if (cm >= 5) setEdgeThickness(P.structSel.idx, cm); } });
        wallDoorFlip.addEventListener('click', (e) => { e.stopPropagation(); flipDoorSide(); });
        wallDoorHinge.addEventListener('click', (e) => { e.stopPropagation(); swapDoorHinge(); });
        wallPop.style.display = 'none'; planEl.append(wallPop);
        wallPop.addEventListener('pointerdown', (e) => e.stopPropagation());
        // Click-to-show room area badge (above vehicles/zones, so it's never overlapped).
        const roomBadge = el('div', { class: 'gp-roombadge' }); roomBadge.style.display = 'none'; planEl.append(roomBadge);
        function positionRoomBadge() { if (!P.roomSel || !P.autoArea) { roomBadge.style.display = 'none'; return; } roomBadge.textContent = 'Raum · ' + P.roomSel.area.toFixed(1).replace('.', ',') + ' m²'; roomBadge.style.left = (P.roomSel.x * P.CELL) + 'px'; roomBadge.style.top = (P.roomSel.y * P.CELL) + 'px'; roomBadge.style.display = 'block'; }
        function showRoom(p) { const rm = roomAt(p); P.roomSel = rm ? { x: p.x, y: p.y, area: rm.area } : null; positionRoomBadge(); return !!rm; }
        wallLenIn.addEventListener('keydown', (e) => { e.stopPropagation(); if (e.key === 'Enter') wallLenIn.blur(); });
        wallLenIn.addEventListener('change', () => { const ss = P.structSel; if (!ss) return; const val = parseFloat(wallLenIn.value); if (!(val > 0.05)) return; if (ss.type === 'edge') setEdgeLength(ss.idx, val); else if (ss.type === 'opening') setOpeningWidth(ss.ei, ss.oi, val); });
        wallPopDel.addEventListener('click', (e) => { e.stopPropagation(); deleteSel(); });
        function positionWallPop() {
            const ss = P.structSel;
            const okEdge = ss && ss.type === 'edge' && P.walls.edges[ss.idx];
            const okOpen = ss && ss.type === 'opening' && P.walls.edges[ss.ei] && (P.walls.edges[ss.ei].ops || [])[ss.oi];
            const okNode = ss && ss.type === 'node' && P.walls.nodes[ss.idx];
            const zone = (!ss && P.sel != null) ? P.excl.find((x) => x.id === P.sel) : null;
            if (!(P.mode === 'plan' && !P.calib && (okEdge || okOpen || okNode || zone))) { wallPop.style.display = 'none'; return; }
            let mx, my, val = null;
            if (okEdge) { const v = edgeVec(P.walls.edges[ss.idx]); mx = (v.a.x + v.b.x) / 2 * P.CELL; my = (v.a.y + v.b.y) / 2 * P.CELL; val = v.len; wallPopLabel.textContent = 'Länge'; }
            else if (okOpen) { const e = P.walls.edges[ss.ei], v = edgeVec(e), o = e.ops[ss.oi]; mx = (v.a.x + v.dx * o.c) * P.CELL; my = (v.a.y + v.dy * o.c) * P.CELL; val = o.w; wallPopLabel.textContent = 'Breite'; }
            else if (okNode) { const n = P.walls.nodes[ss.idx]; mx = n.x * P.CELL; my = n.y * P.CELL; wallPopLabel.textContent = 'Punkt auflösen'; }
            else { mx = (zone.x + zone.w / 2) * P.CELL; my = zone.y * P.CELL; wallPopLabel.textContent = zone.label; } // zone: label + delete
            const showInput = val != null;
            wallLenIn.style.display = showInput ? '' : 'none'; wallPopUnit.style.display = showInput ? '' : 'none';
            const showTh = !!okEdge; [wallThLabel, wallThIn, wallThUnit].forEach((n) => n.style.display = showTh ? '' : 'none');
            if (showTh && document.activeElement !== wallThIn) wallThIn.value = Math.round((P.walls.edges[ss.idx].thick || 0.24) * 100);
            const isDoor = okOpen && P.walls.edges[ss.ei].ops[ss.oi].kind === 'door'; [wallDoorFlip, wallDoorHinge].forEach((n) => n.style.display = isDoor ? '' : 'none');
            wallZoneRot.style.display = zone ? '' : 'none';
            wallPopDel.title = zone ? 'Fläche löschen' : okNode ? 'Punkt auflösen (Wände verbinden)' : okOpen ? 'Öffnung löschen' : 'Segment löschen';
            const pw = P.Wm * P.CELL, ph = P.Hm * P.CELL;
            wallPop.style.left = Math.max(64, Math.min(pw - 64, mx)) + 'px';
            wallPop.style.top = Math.max(30, Math.min(ph - 8, my - 26)) + 'px';
            wallPop.style.display = 'flex';
            if (showInput && document.activeElement !== wallLenIn) wallLenIn.value = val.toFixed(2);
        }
        // (legacy floor-vertex context menu removed — the wall editor replaced it)
        function hideVertMenu() { /* no-op: kept for the shared draw/mode-switch call sites */ }

        // ---- geometry mutations ----
        const snapV = (v, step) => (P.snap ? Math.round(v / step) * step : Math.round(v * 100) / 100);
        const gsnap = (v) => snapV(v, P.gridStep); // snap to the chosen grid step (¼/½/1 m), else free (0.01 m)
        // Rotation-aware AABB half-extents for a block.
        const halfAABB = (bl) => { const rad = (bl.rot || 0) * Math.PI / 180, c = Math.abs(Math.cos(rad)), s = Math.abs(Math.sin(rad)); return { hx: (bl.w * c + bl.h * s) / 2, hy: (bl.w * s + bl.h * c) / 2 }; };
        // Bounding box of the ACTUAL floor polygon (the green outline) — NOT the canvas
        // 0..Wm/0..Hm. The floor is usually inset (e.g. top at y=1.2, right at x=36.1), so
        // clamping/snapping to the canvas put blocks OUTSIDE the green line → reset/gap.
        const floorBB = () => { let a = Infinity, b = Infinity, c = -Infinity, d = -Infinity; for (const [x, y] of P.floor) { if (x < a) a = x; if (x > c) c = x; if (y < b) b = y; if (y > d) d = y; } return { minX: a, minY: b, maxX: c, maxY: d }; };
        // Clamp a block's top-left so its ROTATION-AWARE bounding box stays inside the floor
        // bbox (any angle can sit flush against the real border, incl. the inset ones).
        const clampXY = (bl, nx, ny) => {
            const { hx, hy } = halfAABB(bl), bb = floorBB();
            const cx = Math.max(bb.minX + hx, Math.min(bb.maxX - hx, nx + bl.w / 2)), cy = Math.max(bb.minY + hy, Math.min(bb.maxY - hy, ny + bl.h / 2));
            return { x: cx - bl.w / 2, y: cy - bl.h / 2 };
        };
        // clamp + MAGNETIC auto-snap of the rotated bounding box to the floor's OWN edge
        // coordinates (every unique vertex x / y — outer borders AND the concave step), so a
        // vehicle "klebt" flush at the green line. Reach = one grid cell (+ε) so "drag toward
        // a wall" ALWAYS lands flush instead of stopping one raster cell short.
        const snapPos = (bl, nx, ny) => {
            const cl = clampXY(bl, nx, ny), { hx, hy } = halfAABB(bl), bb = floorBB();
            const cx0 = cl.x + bl.w / 2, cy0 = cl.y + bl.h / 2;
            const SNAP = (P.snap ? (P.gridStep || 0.5) : 0.25) + 0.05;
            const xs = [...new Set(P.floor.map((p) => p[0]))], ys = [...new Set(P.floor.map((p) => p[1]))];
            // Auto-Snap: also fang the dragged block's AABB against EVERY other block's AABB
            // edges (vehicles, walls, lanes, zones alike) so objects click flush to each other,
            // not just to the floor line. Zones are valid snap targets even though they never block.
            if (P.autoSnap) {
                const selfId = bl._id || bl.id;
                for (const o of P.spots.concat(P.excl)) {
                    if ((o._id || o.id) === selfId) continue;
                    const oh = halfAABB(o), ocx = o.x + o.w / 2, ocy = o.y + o.h / 2;
                    xs.push(ocx - oh.hx, ocx + oh.hx); ys.push(ocy - oh.hy, ocy + oh.hy);
                }
                // Snap to the FACES of axis-aligned walls (both faces added; the near one is the
                // inner face for a block sitting inside the room) → vehicles/zones line up flush
                // to the inner wall surface. Use edgePts (the Bezug-offset centreline, exactly as
                // rendered) — NOT the raw node line — so faces land on the real inner surface for
                // Innen/Außen too, not half a wall-thickness off (which left a gap at openings).
                P.walls.edges.forEach((e, ei) => { const vp = edgePts(e, ei); if (vp.len < 0.02) return; const t2 = (e.thick || 0.24) / 2;
                    if (Math.abs(vp.dy) < 0.02) { const cy = (vp.a.y + vp.b.y) / 2; ys.push(cy - t2, cy + t2); }
                    else if (Math.abs(vp.dx) < 0.02) { const cx = (vp.a.x + vp.b.x) / 2; xs.push(cx - t2, cx + t2); }
                });
            }
            // Snap whichever AABB edge (lo/hi) is closest to a floor coordinate within reach.
            const snapAxis = (lo, hi, coords) => { let best = 0, bd = SNAP + 1e-9; for (const co of coords) { const dl = Math.abs(lo - co); if (dl < bd) { bd = dl; best = co - lo; } const dh = Math.abs(hi - co); if (dh < bd) { bd = dh; best = co - hi; } } return bd <= SNAP ? best : 0; };
            const dx = snapAxis(cx0 - hx, cx0 + hx, xs), dy = snapAxis(cy0 - hy, cy0 + hy, ys);
            const clampC = (cx, cy) => [Math.max(bb.minX + hx, Math.min(bb.maxX - hx, cx)), Math.max(bb.minY + hy, Math.min(bb.maxY - hy, cy))];
            const ok = (cx, cy) => inside({ x: cx - bl.w / 2, y: cy - bl.h / 2, w: bl.w, h: bl.h, rot: bl.rot });
            // Take the fullest snap that stays INSIDE the real floor. For diagonal (trap) or
            // notch (u) shapes a flush-to-bbox snap can land outside — fall back (both→x→y→none)
            // so the magnet never pushes a block outside the green line (i.e. never resets).
            for (const [ax, ay] of [[dx, dy], [dx, 0], [0, dy]]) { if (!ax && !ay) continue; const [cx, cy] = clampC(cx0 + ax, cy0 + ay); if (ok(cx, cy)) return { x: cx - bl.w / 2, y: cy - bl.h / 2 }; }
            return { x: cx0 - bl.w / 2, y: cy0 - bl.h / 2 };
        };
        // Resize a block from one of 8 handles with the OPPOSITE side anchored (world-fixed),
        // computed in the block's LOCAL frame so rotated blocks resize along their own axes.
        // o = geometry at drag start {x,y,w,h,rot}; dir ∈ nw n ne e se s sw w; px,py = pointer (m).
        function resizeBlock(o, dir, px, py) {
            const MIN = 0.5, rad = (o.rot || 0) * Math.PI / 180, cos = Math.cos(rad), sin = Math.sin(rad);
            const cx = o.x + o.w / 2, cy = o.y + o.h / 2, dx = px - cx, dy = py - cy;
            const lx = dx * cos + dy * sin + o.w / 2, ly = -dx * sin + dy * cos + o.h / 2; // world → old-local
            let left = 0, right = o.w, top = 0, bottom = o.h;
            if (dir.indexOf('w') >= 0) left = Math.min(gsnap(lx), right - MIN);
            if (dir.indexOf('e') >= 0) right = Math.max(gsnap(lx), left + MIN);
            if (dir.indexOf('n') >= 0) top = Math.min(gsnap(ly), bottom - MIN);
            if (dir.indexOf('s') >= 0) bottom = Math.max(gsnap(ly), top + MIN);
            const nw = right - left, nh = bottom - top, ex = (left + right) / 2 - o.w / 2, ey = (top + bottom) / 2 - o.h / 2;
            const ncx = cx + ex * cos - ey * sin, ncy = cy + ex * sin + ey * cos;
            return { x: ncx - nw / 2, y: ncy - nh / 2, w: nw, h: nh };
        }
        // 8 resize handles + the rotate knob on the selected, editable block.
        function addHandles(d, id) {
            ['nw', 'n', 'ne', 'e', 'se', 's', 'sw', 'w'].forEach((dir) => { const h = el('div', { class: 'gp-rz gp-rz-' + dir }); h.dataset.rz = id; h.dataset.dir = dir; d.append(h); });
            const rot = el('div', { class: 'gp-rot', title: 'Drehen (Snap 15°, frei mit Alt)' }); rot.dataset.rotid = id; d.append(rot);
        }
        // After a free floor edit, re-home the polygon into [0,Wm]×[0,Hm]: shift any
        // negative coords to origin and grow Wm/Hm to fit, so enlarging an edge works.
        function refit() { let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity; P.floor.forEach(([x, y]) => { minX = Math.min(minX, x); minY = Math.min(minY, y); maxX = Math.max(maxX, x); maxY = Math.max(maxY, y); }); const dx = minX < 0 ? -minX : 0, dy = minY < 0 ? -minY : 0; if (dx || dy) { P.floor = P.floor.map(([x, y]) => [x + dx, y + dy]); P.excl.forEach((e) => { e.x += dx; e.y += dy; }); P.spots.forEach((s) => { s.x += dx; s.y += dy; s._dirty = true; }); maxX += dx; maxY += dy; } P.Wm = Math.max(6, Math.ceil(maxX - 1e-6)); P.Hm = Math.max(5, Math.ceil(maxY - 1e-6)); clampAll(); }

        // ---- undo/redo (index-based history over hall geometry + placement
        // geometry, not existence). The current state always lives at hist[hpos];
        // pushUndo() records the state AFTER a mutation, undo/redo step the pointer. ----
        const snapshot = () => JSON.stringify({ floor: P.floor, Wm: P.Wm, Hm: P.Hm, tor: P.tor, load: P.load, shape: P.shape, excl: P.excl, walls: P.walls, spots: P.spots.map((s) => ({ _id: s._id, x: s.x, y: s.y, w: s.w, h: s.h, rot: s.rot, status: s.status, noBuf: !!s.noBuf })) });
        // restore reverts local state; a spot is flagged _dirty ONLY when a value
        // actually changed, so undo/redo can re-persist exactly those to the server.
        function restore(js) {
            const st = JSON.parse(js); P.floor = st.floor; P.Wm = st.Wm; P.Hm = st.Hm; P.tor = st.tor; P.load = st.load; P.shape = st.shape; P.excl = st.excl; if (st.walls) P.walls = st.walls; P.structSel = null;
            st.spots.forEach((g) => { const s = P.spots.find((x) => x._id === g._id); if (!s) return;
                if (s.x !== g.x || s.y !== g.y || s.w !== g.w || s.h !== g.h || s.rot !== g.rot || s.status !== g.status || !!s.noBuf !== !!g.noBuf) { s.x = g.x; s.y = g.y; s.w = g.w; s.h = g.h; s.rot = g.rot; s.status = g.status; s.noBuf = !!g.noBuf; s._dirty = true; } });
        }
        function pushUndo() { P.hist = P.hist.slice(0, P.hpos + 1); P.hist.push(snapshot()); if (P.hist.length > 80) P.hist.shift(); P.hpos = P.hist.length - 1; }
        function commitGeom(msg, kind) { pushUndo(); markDirty(); draw(); if (msg) toast(msg, kind || ''); }
        let saveTimer = null;
        // Auto-save: geometry/placement edits are batched (atomic floor+spots, so an
        // undo across a refit can't desync them) but persisted automatically on a
        // short debounce, so nothing is lost on navigation. The Save button is a
        // manual "save now".
        function scheduleSave() { clearTimeout(saveTimer); saveTimer = setTimeout(() => { if (P.dirty) doSaveGeom(true); }, 800); }
        function markDirty() { P.dirty = true; scheduleSave(); }
        function doUndo() { if (P.hpos <= 0) return; P.hpos--; restore(P.hist[P.hpos]); markDirty(); P.sel = null; hideLen(); draw(); toast('Rückgängig'); }
        function doRedo() { if (P.hpos >= P.hist.length - 1) return; P.hpos++; restore(P.hist[P.hpos]); markDirty(); P.sel = null; hideLen(); draw(); toast('Wiederholt'); }
        let spaceDown = false; // Space held → left-drag pans instead of moving a block
        const keyHandler = (e) => {
            if (!root.isConnected) { window.removeEventListener('keydown', keyHandler); return; }
            const m = e.ctrlKey || e.metaKey, tag = (e.target.tagName || '').toLowerCase(), typing = tag === 'input' || tag === 'textarea' || tag === 'select' || e.target.isContentEditable;
            if (m && e.key.toLowerCase() === 'z') { e.preventDefault(); e.shiftKey ? doRedo() : doUndo(); }
            else if (m && e.key.toLowerCase() === 'y') { e.preventDefault(); doRedo(); }
            else if (e.key === 'Escape') { if (shortcutModal) { e.preventDefault(); toggleShortcutHelp(); } else if (P.tool || P.chain || P.openStart || zoneDraw) { e.preventDefault(); cancelDrawing(); } else if (selSet.length) { e.preventDefault(); clearSelSet(); draw(); } else if (P.structSel || P.sel != null) { e.preventDefault(); P.structSel = null; P.sel = null; draw(); } else if (P.maxed) { toggleMax(false); } }
            else if (!typing && !m && e.key === '?') { e.preventDefault(); toggleShortcutHelp(); } // ? = Tastaturkürzel-Hilfe (QW3)
            else if (!typing && (e.key === 'Delete' || e.key === 'Backspace') && (selSet.length || (P.mode === 'plan' && (P.structSel || P.sel != null)))) { e.preventDefault(); deleteSel(); }
            else if (!typing && !m && e.key.toLowerCase() === 'f' && doorSel()) { e.preventDefault(); flipDoorSide(); } // F = Tür spiegeln (bei ausgewählter Tür)
            else if (!typing && !m && e.key === ' ' && doorSel()) { e.preventDefault(); swapDoorHinge(); } // Space = Anschlag wechseln (bei ausgewählter Tür)
            else if (!typing && !m && e.key.toLowerCase() === 'r' && P.mode === 'plan' && P.sel != null) { const b = P.excl.find((x) => x.id === P.sel); if (b) { e.preventDefault(); b.rot = ((b.rot || 0) + 90) % 360; commitGeom('Fläche gedreht'); } } // R = Fläche 90° drehen
            else if (!typing && !m && e.key === 'p' && P.mode === 'plan') { e.preventDefault(); P.buffer = !P.buffer; renderToolbar(); draw(); toast(P.buffer ? 'Pufferzonen an' : 'Pufferzonen aus'); } // P = Puffer-Toggle
            else if (!typing && !m && e.key.toLowerCase() === 'f') { e.preventDefault(); fitView(); toast('Eingepasst'); } // F = Einpassen
            else if (!typing && !m && e.key === ' ') { e.preventDefault(); if (!spaceDown) { spaceDown = true; planWrap.style.cursor = 'grab'; } } // Space = Pan-Modus
        };
        const keyUpHandler = (e) => { if (!root.isConnected) { window.removeEventListener('keyup', keyUpHandler); return; } if (e.key === ' ') { spaceDown = false; if (!midPan) planWrap.style.cursor = ''; } };

        // QW3 — discoverable keyboard-shortcut overlay (open with "?" or the toolbar button).
        let shortcutModal = null;
        function toggleShortcutHelp() {
            if (shortcutModal) { shortcutModal.remove(); shortcutModal = null; return; }
            const rows = [
                ['Ctrl / ⌘ + Z', 'Rückgängig'],
                ['Ctrl / ⌘ + ⇧ + Z · Ctrl + Y', 'Wiederholen'],
                ['F', 'Einpassen (Zoom-to-fit) — bei ausgewählter Tür: spiegeln'],
                ['Leertaste (halten) + ziehen', 'Ansicht verschieben — bei ausgewählter Tür: Anschlag wechseln'],
                ['Mittlere Maustaste + ziehen', 'Ansicht verschieben (Pan)'],
                ['Mausrad', 'Zoomen'],
                ['⇧ beim Ziehen', 'Ortho-Snap (horizontal / vertikal)'],
                ['R', 'Ausgewählte Fläche 90° drehen'],
                ['P', 'Pufferzonen an / aus'],
                ['Entf / ⌫', 'Auswahl löschen'],
                ['Esc', 'Zeichnen abbrechen · Auswahl aufheben · Vollbild verlassen'],
                ['?', 'Diese Hilfe öffnen / schließen'],
            ];
            const modal = el('div', { class: 'gp-help-backdrop', role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Tastaturkürzel' });
            const card = el('div', { class: 'gp-help-card' });
            card.append(el('div', { class: 'gp-help-head' }, el('h3', {}, '⌨ Tastaturkürzel'), el('button', { class: 'gp-help-x', 'aria-label': 'Schließen', onclick: () => toggleShortcutHelp() }, '✕')));
            const list = el('div', { class: 'gp-help-list' });
            rows.forEach(([k, d]) => { list.append(el('kbd', { class: 'gp-help-k' }, k), el('span', { class: 'gp-help-d' }, d)); });
            card.append(list, el('div', { class: 'gp-help-foot' }, 'Werkzeuge liegen rechts in der Leiste · „Passen" zentriert den Plan.'));
            modal.append(card);
            modal.addEventListener('click', (ev) => { if (ev.target === modal) toggleShortcutHelp(); });
            (root || document.body).append(modal);
            shortcutModal = modal;
        }

        // ---- FE1/FE2: plan export. A dedicated, self-contained renderer (inline styles, white
        // ground, dark-on-white) feeds PNG (raster), PDF (browser print) and SVG; DXF comes from
        // the raw geometry. Independent of the live theme so printouts always read cleanly. ----
        const xmlEsc = (s) => String(s == null ? '' : s).replace(/[<>&"]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;' }[c]));
        function planExportSVG() {
            const bb = contentBBox(); if (!bb) return null;
            const S = 40, pad = 1.0, titleH = 70; // px/m, metre margin, title-block height
            const cw = (bb.maxX - bb.minX) + 2 * pad, ch = (bb.maxY - bb.minY) + 2 * pad;
            const W = cw * S, H = ch * S, ox = pad - bb.minX, oy = pad - bb.minY;
            const X = (mx) => ((mx + ox) * S).toFixed(1), Y = (my) => ((my + oy) * S).toFixed(1);
            let s = '<rect x="0" y="0" width="' + W.toFixed(1) + '" height="' + (H + titleH).toFixed(1) + '" fill="#ffffff"/>';
            // walls (mitre-joined polygons, split at openings) + opening marks
            const wcs = wallCorners();
            P.walls.edges.forEach((e, ei) => {
                const wc = wcs[ei]; if (!wc) return;
                const oa = { x: (wc.aL.x + wc.aR.x) / 2, y: (wc.aL.y + wc.aR.y) / 2 }, ob = { x: (wc.bL.x + wc.bR.x) / 2, y: (wc.bL.y + wc.bR.y) / 2 };
                const vlen = Math.hypot(ob.x - oa.x, ob.y - oa.y) || 1, hw = (e.thick || 0.24) / 2, px = -(ob.y - oa.y) / vlen, py = (ob.x - oa.x) / vlen;
                const lA = (u) => u <= 0 ? [wc.aL.x, wc.aL.y] : u >= 1 ? [wc.bL.x, wc.bL.y] : [oa.x + (ob.x - oa.x) * u + px * hw, oa.y + (ob.y - oa.y) * u + py * hw];
                const rA = (u) => u <= 0 ? [wc.aR.x, wc.aR.y] : u >= 1 ? [wc.bR.x, wc.bR.y] : [oa.x + (ob.x - oa.x) * u - px * hw, oa.y + (ob.y - oa.y) * u - py * hw];
                const spans = (e.ops || []).map((o) => { const hp = (o.w / vlen) / 2; return { lo: Math.max(0, o.c - hp), hi: Math.min(1, o.c + hp) }; }).sort((a, b) => a.lo - b.lo);
                const piece = (u0, u1) => { if (u1 - u0 < 1e-4) return; const l0 = lA(u0), l1 = lA(u1), r1 = rA(u1), r0 = rA(u0); s += '<polygon points="' + X(l0[0]) + ',' + Y(l0[1]) + ' ' + X(l1[0]) + ',' + Y(l1[1]) + ' ' + X(r1[0]) + ',' + Y(r1[1]) + ' ' + X(r0[0]) + ',' + Y(r0[1]) + '" fill="#3a4656"/>'; };
                let t0 = 0; spans.forEach((sp) => { piece(t0, sp.lo); t0 = sp.hi; }); piece(t0, 1);
                spans.forEach((sp) => { const mc = (sp.lo + sp.hi) / 2, c = { x: oa.x + (ob.x - oa.x) * mc, y: oa.y + (ob.y - oa.y) * mc }; s += '<line x1="' + X(c.x + px * hw) + '" y1="' + Y(c.y + py * hw) + '" x2="' + X(c.x - px * hw) + '" y2="' + Y(c.y - py * hw) + '" stroke="#2b7fd0" stroke-width="2"/>'; });
            });
            // zones + structural blocks (columns), then vehicles, then room m²
            P.excl.forEach((b) => { const q = quad(b).map((p) => X(p[0]) + ',' + Y(p[1])).join(' '), zone = isZoneKind(b.kind);
                s += '<polygon points="' + q + '" fill="' + (zone ? 'rgba(47,122,90,0.14)' : '#6b7686') + '" stroke="' + (zone ? '#2f9e6b' : '#4a5666') + '" stroke-width="1"' + (zone ? ' stroke-dasharray="5 3"' : '') + '/>';
                if (zone) s += '<text x="' + X(b.x + b.w / 2) + '" y="' + Y(b.y + b.h / 2) + '" text-anchor="middle" font-size="10" fill="#245a3f">' + xmlEsc(b.label) + '</text>'; });
            P.spots.forEach((b) => { const q = quad(b).map((p) => X(p[0]) + ',' + Y(p[1])).join(' '), lab = b.personName || b.label || b.type || '';
                s += '<polygon points="' + q + '" fill="rgba(70,130,180,0.26)" stroke="#2b6cb0" stroke-width="1.4"/>';
                if (lab) s += '<text x="' + X(b.x + b.w / 2) + '" y="' + Y(b.y + b.h / 2 + 0.12) + '" text-anchor="middle" font-size="10" fill="#173a5a">' + xmlEsc(lab) + '</text>'; });
            const enc = enclosure();
            if (enc && enc.rooms) enc.rooms.forEach((rm, i) => { if (rm.area > 0.5) s += '<text x="' + X(rm.cx) + '" y="' + Y(rm.cy) + '" text-anchor="middle" font-size="12" font-weight="700" fill="#2f7a5a">' + (enc.rooms.length > 1 ? 'Raum ' + (i + 1) + ' · ' : '') + rm.area.toFixed(1).replace('.', ',') + ' m²</text>'; });
            // title block: name, date, total m², and a 1 m scale bar
            const total = enc && enc.area ? enc.area.toFixed(1).replace('.', ',') + ' m²' : '—';
            const name = P.hallName || P.name || 'Grundriss', ty = H + 4;
            let tb = '<g transform="translate(0,' + ty.toFixed(1) + ')">';
            tb += '<rect x="0" y="0" width="' + W.toFixed(1) + '" height="' + titleH + '" fill="#f4f6f8" stroke="#cbd5e1"/>';
            tb += '<text x="14" y="27" font-size="15" font-weight="700" fill="#1f2937">Parkrr · ' + xmlEsc(name) + '</text>';
            tb += '<text x="14" y="48" font-size="11" fill="#4b5563">' + new Date().toLocaleDateString('de-DE') + ' · Parkfläche ' + total + '</text>';
            const bx = W - 14 - S; // 1 m scale bar
            tb += '<line x1="' + bx.toFixed(1) + '" y1="44" x2="' + (bx + S).toFixed(1) + '" y2="44" stroke="#1f2937" stroke-width="2"/>';
            tb += '<line x1="' + bx.toFixed(1) + '" y1="40" x2="' + bx.toFixed(1) + '" y2="48" stroke="#1f2937" stroke-width="2"/><line x1="' + (bx + S).toFixed(1) + '" y1="40" x2="' + (bx + S).toFixed(1) + '" y2="48" stroke="#1f2937" stroke-width="2"/>';
            tb += '<text x="' + (bx + S / 2).toFixed(1) + '" y="60" text-anchor="middle" font-size="10" fill="#4b5563">1 m</text></g>';
            s += tb;
            return { body: s, W: W, H: H + titleH, svg: '<svg xmlns="http://www.w3.org/2000/svg" width="' + W.toFixed(0) + '" height="' + (H + titleH).toFixed(0) + '" viewBox="0 0 ' + W.toFixed(1) + ' ' + (H + titleH).toFixed(1) + '" font-family="system-ui,Segoe UI,Roboto,Arial,sans-serif">' + s + '</svg>' };
        }
        function dlBlob(name, data, mime) { const blob = data instanceof Blob ? data : new Blob([data], { type: mime }); const u = URL.createObjectURL(blob); const a = el('a', { href: u, download: name }); document.body.append(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(u), 4000); }
        function exportPlanPNG() { const ex = planExportSVG(); if (!ex) { toast('Nichts zu exportieren', 'warn'); return; } const scale = 2, cv = el('canvas'); cv.width = Math.round(ex.W * scale); cv.height = Math.round(ex.H * scale); const cx = cv.getContext('2d'); const img = new Image();
            img.onload = () => { cx.setTransform(scale, 0, 0, scale, 0, 0); cx.drawImage(img, 0, 0); cv.toBlob((b) => { if (b) { dlBlob('grundriss.png', b, 'image/png'); toast('PNG exportiert', 'ok'); } }, 'image/png'); };
            img.onerror = () => toast('PNG-Export fehlgeschlagen', 'error'); img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(ex.svg); }
        function exportPlanSVG() { const ex = planExportSVG(); if (!ex) { toast('Nichts zu exportieren', 'warn'); return; } dlBlob('grundriss.svg', ex.svg, 'image/svg+xml'); toast('SVG exportiert', 'ok'); }
        function exportPlanPDF() { const ex = planExportSVG(); if (!ex) { toast('Nichts zu exportieren', 'warn'); return; } const w = window.open('', '_blank'); if (!w) { toast('Popup blockiert — bitte erlauben', 'warn'); return; }
            // No inline <script> in the popup — the opener's CSP (script-src 'self') would block it.
            // We drive print/close from here after the document is written.
            w.document.write('<!doctype html><meta charset="utf-8"><title>Grundriss</title><style>@page{margin:12mm}html,body{margin:0}svg{width:100%;height:auto}@media print{.hint{display:none}}</style><div class="hint" style="font:13px system-ui;padding:8px;color:#555">Drucken-Dialog → „Als PDF speichern".</div>' + ex.svg);
            w.document.close();
            try { w.onafterprint = () => { try { w.close(); } catch (e) { /* ignore */ } }; } catch (e) { /* ignore */ }
            setTimeout(() => { try { w.focus(); w.print(); } catch (e) { /* ignore */ } }, 300); }
        function exportPlanDXF() {
            if (!P.walls.edges.length && !P.spots.length && !P.excl.length) { toast('Nichts zu exportieren', 'warn'); return; }
            const L = []; const P0 = (x, y) => x.toFixed(3) + '\n20\n' + (-y).toFixed(3); // DXF is Y-up → flip
            const line = (a, b, layer) => L.push('0\nLINE\n8\n' + layer + '\n10\n' + P0(a.x, a.y) + '\n30\n0\n11\n' + P0(b.x, b.y) + '\n31\n0');
            const pline = (pts, layer) => { let e = '0\nLWPOLYLINE\n8\n' + layer + '\n90\n' + pts.length + '\n70\n1'; pts.forEach((p) => { e += '\n10\n' + p[0].toFixed(3) + '\n20\n' + (-p[1]).toFixed(3); }); L.push(e); };
            P.walls.edges.forEach((e) => { const v = edgeVec(e); if (v.len < 0.02) return; line(v.a, v.b, 'WALLS'); (e.ops || []).forEach((o) => { const hp = (o.w / v.len) / 2, a = { x: v.a.x + v.dx * (o.c - hp), y: v.a.y + v.dy * (o.c - hp) }, b = { x: v.a.x + v.dx * (o.c + hp), y: v.a.y + v.dy * (o.c + hp) }; line(a, b, 'OPENINGS'); }); });
            P.excl.forEach((b) => pline(quad(b), isZoneKind(b.kind) ? 'ZONES' : 'STRUCT'));
            P.spots.forEach((b) => pline(quad(b), 'VEHICLES'));
            const dxf = '0\nSECTION\n2\nENTITIES\n' + L.join('\n') + '\n0\nENDSEC\n0\nEOF\n';
            dlBlob('grundriss.dxf', dxf, 'application/dxf'); toast('DXF exportiert', 'ok');
        }
        function openExportMenu() {
            const opts = [['🖼 PNG', exportPlanPNG], ['📄 PDF (Drucken)', exportPlanPDF], ['⬔ SVG (Vektor)', exportPlanSVG], ['📐 DXF (CAD)', exportPlanDXF]];
            const modal = el('div', { class: 'gp-help-backdrop' });
            const card = el('div', { class: 'gp-help-card', style: 'max-width:320px' });
            card.append(el('div', { class: 'gp-help-head' }, el('h3', {}, '⭳ Exportieren'), el('button', { class: 'gp-help-x', 'aria-label': 'Schließen', onclick: () => modal.remove() }, '✕')));
            const list = el('div', { style: 'display:flex;flex-direction:column;gap:.4rem;padding:16px 18px' });
            opts.forEach(([lab, fn]) => list.append(el('button', { class: 'gp-tbtn', style: 'justify-content:flex-start', onclick: () => { modal.remove(); fn(); } }, lab)));
            card.append(list); modal.append(card); modal.addEventListener('click', (ev) => { if (ev.target === modal) modal.remove(); }); (root || document.body).append(modal);
        }

        // ---- FE3: reusable wall-layout templates (the building shape), stored client-side. ----
        const TPL_KEY = 'parkrr.wallTemplates';
        const loadTemplates = () => { try { const a = JSON.parse(localStorage.getItem(TPL_KEY) || '[]'); return Array.isArray(a) ? a : []; } catch (e) { return []; } };
        const saveTemplates = (a) => { try { localStorage.setItem(TPL_KEY, JSON.stringify(a.slice(0, 30))); } catch (e) { /* quota */ } };
        async function saveCurrentTemplate() {
            if (!P.walls.edges.length) { toast('Keine Wände zum Speichern', 'warn'); return; }
            const data = await formModal({ title: 'Wand-Vorlage speichern', submitLabel: 'Speichern', fields: [{ name: 'name', label: 'Name der Vorlage', type: 'text', required: true, value: (P.hallName || 'Vorlage') + ' ' + (loadTemplates().length + 1) }] });
            if (!data || !data.name) return;
            const tpls = loadTemplates(); tpls.unshift({ id: Date.now(), name: String(data.name).slice(0, 60), walls: JSON.parse(JSON.stringify(P.walls)) }); saveTemplates(tpls); toast('Vorlage gespeichert', 'ok');
        }
        async function applyTemplate(t) {
            if (P.walls.edges.length && !(await confirmDialog('Vorlage laden', 'Aktuelle Wände durch die Vorlage „' + t.name + '" ersetzen?', 'Ersetzen'))) return;
            P.walls = normalizeWalls(t.walls); P.structSel = null; P.sel = null; refreshFloorFromWalls(); pushUndo(); markDirty(); fitView(); toast('Vorlage geladen: ' + t.name, 'ok');
        }
        function openTemplateMenu() {
            const modal = el('div', { class: 'gp-help-backdrop' });
            const card = el('div', { class: 'gp-help-card', style: 'max-width:380px' });
            card.append(el('div', { class: 'gp-help-head' }, el('h3', {}, '▤ Wand-Vorlagen'), el('button', { class: 'gp-help-x', 'aria-label': 'Schließen', onclick: () => modal.remove() }, '✕')));
            const body = el('div', { style: 'display:flex;flex-direction:column;gap:.45rem;padding:16px 18px' });
            body.append(el('button', { class: 'gp-tbtn', style: 'justify-content:flex-start', onclick: () => { modal.remove(); saveCurrentTemplate(); } }, '＋ Aktuelle Wände als Vorlage speichern'));
            const tpls = loadTemplates();
            if (!tpls.length) body.append(el('div', { class: 'muted', style: 'font-size:.82rem;padding:.3rem 0' }, 'Noch keine Vorlagen. Zeichne Wände und speichere sie hier.'));
            tpls.forEach((t) => {
                const row = el('div', { style: 'display:flex;gap:.4rem;align-items:center' });
                row.append(el('button', { class: 'gp-tbtn', style: 'flex:1;justify-content:flex-start', onclick: () => { modal.remove(); applyTemplate(t); } }, '▤ ' + t.name + ' · ' + (t.walls && t.walls.edges ? t.walls.edges.length : 0) + ' Wände'));
                row.append(el('button', { class: 'gp-help-x', title: 'Löschen', onclick: () => { saveTemplates(loadTemplates().filter((x) => x.id !== t.id)); modal.remove(); openTemplateMenu(); } }, '🗑'));
                body.append(row);
            });
            card.append(body); modal.append(card); modal.addEventListener('click', (ev) => { if (ev.target === modal) modal.remove(); }); (root || document.body).append(modal);
        }
        window.addEventListener('keydown', keyHandler);
        window.addEventListener('keyup', keyUpHandler);

        function hideLen() { /* no-op stub — kept for the shared undo/mode-switch call sites */ }

        // ---- layout / sizing ----
        function layout() {
            hideVertMenu(); // a relayout (zoom/resize) would detach the pinned menu from its vertex
            const M = 40; // scrollable free space around the floor
            // maxBudget is the tallest the wrapper may get. Fit derives ONLY from availW
            // and maxBudget (both stable), never from the wrapper's own height → no
            // scrollbar/height feedback loop.
            const maxBudget = P.maxed ? Math.max(260, Math.floor(window.innerHeight - planWrap.getBoundingClientRect().top - 14))
                : Math.min(720, Math.max(320, Math.round(window.innerHeight * 0.72)));
            const availW = (planWrap.clientWidth || 640) - 2 * M, availH = maxBudget - 2 * M;
            const fit = Math.max(6, Math.floor(Math.min(availW / P.Wm, availH / P.Hm)));
            // The on-screen scale (px/m) is a STABLE base (fit-to-content once), so growing or
            // shrinking the canvas bounds no longer rescales the view — only fitView() refits.
            if (P.base == null) P.base = fit;
            P.CELL = Math.max(6, Math.round(P.base * (P.zoom || 1)));
            const pw = P.Wm * P.CELL, ph = P.Hm * P.CELL;
            planEl.style.width = pw + 'px'; planEl.style.height = ph + 'px';
            svg.setAttribute('width', pw); svg.setAttribute('height', ph); svg.setAttribute('viewBox', '0 0 ' + pw + ' ' + ph);
            // Fullscreen: fill the budget (big canvas). Normal: wrap the plan tightly
            // (+margins) up to the budget, so a short hall isn't marooned in a tall box.
            const boxH = P.maxed ? maxBudget : Math.min(maxBudget, ph + 2 * M + 2); // +2 = the wrapper's 1px borders (border-box), else a 2px scrollbar shows
            planWrap.style.height = boxH + 'px';
            // Centre the floor in its wrapper: split the spare space to both sides so a plan that
            // doesn't fill the viewport sits centred (no lopsided empty band). Margins collapse to
            // 0 the moment the content overflows, so panning/scrolling is unaffected.
            const freeW = Math.max(0, (planWrap.clientWidth || pw) - pw), freeH = Math.max(0, boxH - ph);
            planEl.style.marginLeft = Math.floor(freeW / 2) + 'px';
            planEl.style.marginTop = Math.floor(freeH / 2) + 'px';
            draw();
        }
        const resizeH = () => { if (!root.isConnected) { window.removeEventListener('resize', resizeH); return; } layout(); };
        window.addEventListener('resize', resizeH);
        // ResizeObserver catches browser zoom (Ctrl+/-) and container-size changes the
        // 'resize' event can miss, so the plan always refits (no spurious scrollbar).
        let roTimer = null, roLast = '';
        const ro = new ResizeObserver((entries) => {
            if (!root.isConnected) { ro.disconnect(); return; }
            // Observe the BORDER box: it doesn't change when a scrollbar toggles, so a
            // scrollbar appearing can't feed back into a relayout (that was the flicker).
            const e0 = entries[0], bs = e0 && e0.borderBoxSize && e0.borderBoxSize[0];
            const key = bs ? Math.round(bs.inlineSize) + 'x' + Math.round(bs.blockSize)
                : (e0 && e0.contentRect ? Math.round(e0.contentRect.width) + 'x' + Math.round(e0.contentRect.height) : '');
            if (key === roLast) return; roLast = key;
            clearTimeout(roTimer); roTimer = setTimeout(layout, 60);
        });
        try { ro.observe(planWrap, { box: 'border-box' }); } catch (e) { try { ro.observe(planWrap); } catch (e2) { /* ignore */ } }

        // ---- CAD-standard navigation: Ctrl+wheel = zoom to cursor, middle-drag = pan ----
        function zoomAt(clientX, clientY, factor) {
            const nz = Math.max(0.4, Math.min(8, +(P.zoom * factor).toFixed(3)));
            if (nz === P.zoom) return;
            const rB = planEl.getBoundingClientRect();
            const wx = (clientX - rB.left) / P.CELL, wy = (clientY - rB.top) / P.CELL; // world point under cursor
            P.zoom = nz; layout();
            const rA = planEl.getBoundingClientRect(); // adjust scroll so that world point stays under the cursor
            planWrap.scrollLeft += rA.left - (clientX - wx * P.CELL);
            planWrap.scrollTop += rA.top - (clientY - wy * P.CELL);
        }
        planWrap.addEventListener('wheel', (e) => { if (!e.ctrlKey) return; e.preventDefault(); zoomAt(e.clientX, e.clientY, e.deltaY < 0 ? 1.15 : 1 / 1.15); }, { passive: false });
        let midPan = null;
        planWrap.addEventListener('mousedown', (e) => { if (e.button === 1) e.preventDefault(); }); // suppress middle-click autoscroll
        planWrap.addEventListener('pointerdown', (e) => { if (e.button !== 1 && !(e.button === 0 && spaceDown)) return; e.preventDefault(); midPan = { sx: e.clientX, sy: e.clientY, sl: planWrap.scrollLeft, st: planWrap.scrollTop }; try { planWrap.setPointerCapture(e.pointerId); } catch (er) { /* ignore */ } planWrap.style.cursor = 'grabbing'; }); // middle-mouse OR Space+leftclick pans
        planWrap.addEventListener('pointermove', (e) => { if (!midPan) return; planWrap.scrollLeft = midPan.sl - (e.clientX - midPan.sx); planWrap.scrollTop = midPan.st - (e.clientY - midPan.sy); });
        const endMidPan = (e) => { if (midPan) { midPan = null; try { planWrap.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ } planWrap.style.cursor = spaceDown ? 'grab' : ''; } };
        planWrap.addEventListener('pointerup', endMidPan);
        planWrap.addEventListener('pointercancel', endMidPan);

        // ---- draw ----
        function ptsAttr() { return P.floor.map((p) => (p[0] * P.CELL) + ',' + (p[1] * P.CELL)).join(' '); }
        function draw() {
            drawFloor(); drawBlocks(); renderMetrics(); renderRail(); renderToolbar();
            // In plan mode the overlay owns all interaction (draw when a tool is armed, select/
            // edit walls + zones otherwise). Manage mode leaves it off so the vehicle layer works.
            const drawActive = P.mode === 'plan' && !P.calib && canManageNow;
            drawOverlay.style.display = drawActive ? 'block' : 'none';
            drawOverlay.classList.toggle('edit', !P.tool);
            positionWallPop(); positionRoomBadge();
            const used = P.spots.length;
            occN.textContent = used + ' Gefährte';
            const tot = polyArea(P.floor); let ve = 0; P.spots.forEach((b) => { ve += b.w * b.h; });
            occBar.style.width = Math.round(Math.min(100, ve / Math.max(1, tot) * 100)) + '%';
        }
        function drawFloor() {
            svg.innerHTML = '';
            svg.style.zIndex = '0'; // the SVG stays below the interaction overlay (walls define the plan)
            const CELL = P.CELL, s = ptsAttr();
            // grid pattern
            const defs = svgEl('defs');
            const pat = svgEl('pattern', { id: 'gpgrid', width: CELL, height: CELL, patternUnits: 'userSpaceOnUse' });
            pat.append(svgEl('path', { d: 'M' + CELL + ' 0H0V' + CELL, fill: 'none', class: 'gp-gridline' }));
            const clip = svgEl('clipPath', { id: 'gpclip' }); clip.append(svgEl('rect', { x: 0, y: 0, width: P.Wm * CELL, height: P.Hm * CELL }));
            defs.append(pat, clip); svg.append(defs);
            // Neutral drawing surface — the green hall frame is gone; walls define everything.
            svg.append(svgEl('rect', { class: 'gp-canvasbg', width: P.Wm * CELL, height: P.Hm * CELL }));
            // Bauplan underlay — below grid + walls + blocks; unclipped/brighter while calibrating.
            if (P.plan && !P.plan.hidden && P.plan.href) {
                const im = svgEl('image', { class: 'gp-planimg', x: P.plan.x * CELL, y: P.plan.y * CELL, width: Math.max(1, P.plan.w * CELL), height: Math.max(1, P.plan.h * CELL), preserveAspectRatio: 'none', opacity: P.calib ? Math.max(0.7, P.plan.opacity) : P.plan.opacity });
                if (!P.calib) im.setAttribute('clip-path', 'url(#gpclip)');
                im.setAttribute('href', P.plan.href); im.setAttribute('preserveAspectRatio', 'none');
                svg.append(im);
            }
            if (P.grid !== false) svg.append(svgEl('rect', { width: P.Wm * CELL, height: P.Hm * CELL, fill: 'url(#gpgrid)' }));
            drawEnclosure(); // shade the wall-enclosed parking area (+ m² label)
            drawWalls();      // wall segments, openings and (in edit mode) node handles
            drawBuffers();    // per-side vehicle clearance bands (when enabled)
            drawWallPreview();// live chained-drawing / opening preview
            // UX3 — onboarding hint on an empty plan (nothing drawn yet, edit-capable, not mid-draw).
            if (P.mode === 'plan' && canManageNow && P.walls.edges.length === 0 && P.excl.length === 0 && !P.chain && !P.tool) {
                const cx = P.Wm * CELL / 2, cy = P.Hm * CELL / 2;
                const g = svgEl('g', { class: 'gp-emptyhint', 'text-anchor': 'middle' });
                g.append(svgEl('path', { d: 'M' + (cx - 48) + ' ' + (cy - 46) + ' h96 v40', class: 'gp-eh-ic' }));
                const t1 = svgEl('text', { x: cx, y: cy + 4, class: 'gp-eh-t1' }); t1.textContent = 'Zeichne die erste Außenwand';
                const t2 = svgEl('text', { x: cx, y: cy + 26, class: 'gp-eh-t2' }); t2.textContent = 'Werkzeug „Außenwand" wählen · Klick · Klick · Startpunkt schließt den Raum';
                const t3 = svgEl('text', { x: cx, y: cy + 44, class: 'gp-eh-t2' }); t3.textContent = 'Tastaturkürzel: ?';
                g.append(t1, t2, t3); svg.append(g);
            }
        }
        function positionBlock(d, b) {
            const bw = b.w * P.CELL, bh = b.h * P.CELL;
            d.style.left = (b.x * P.CELL) + 'px'; d.style.top = (b.y * P.CELL) + 'px'; d.style.width = bw + 'px'; d.style.height = bh + 'px';
            // Declutter small blocks so labels don't overflow into neighbours.
            d.classList.toggle('gp-small', bh < 42 || bw < 62); // toggle (not add) so a live resize
            d.classList.toggle('gp-tiny', bh < 26 || bw < 42);  // re-shows labels when the block grows
            // Rotate the block; drawBlocks gives rotated blocks a single upright label
            // group (.gp-rlabel). Counter-rotate it AND push it to the block's (screen-)
            // bottom edge instead of the centre, so the info sits under the vehicle rather
            // than on top of it. The push direction is the block's local axis that maps to
            // screen-down after the rotation: R(-rot)·(0, dy) = (sin·dy, cos·dy).
            if (b.rot) {
                d.style.transform = 'rotate(' + b.rot + 'deg)';
                const g = d.querySelector('.gp-rlabel');
                if (g) {
                    const rad = b.rot * Math.PI / 180;
                    const shh = (Math.abs(bw * Math.sin(rad)) + Math.abs(bh * Math.cos(rad))) / 2; // screen half-height
                    const lh = (g.children.length || 3) * 12 + 6; // ~label height (not yet in DOM to measure)
                    const dy = Math.max(0, shh - lh / 2 - 4); // just inside the bottom edge, not the centre
                    g.style.left = (bw / 2 + Math.sin(rad) * dy) + 'px';
                    g.style.top = (bh / 2 + Math.cos(rad) * dy) + 'px';
                    g.style.transform = 'translate(-50%,-50%) rotate(' + (-b.rot) + 'deg)';
                }
            }
        }
        function drawBlocks() {
            layer.innerHTML = '';
            P.spots.forEach((b) => {
                const ctx = !interactive(b), st = GPSTAT[b.status] || GPSTAT.busy;
                let eff = P.render || 'symbol'; if (eff === 'foto' && !b.photoUrl) eff = 'symbol'; // fallback chain: no photo → symbol
                const d = el('div', { class: 'gp-block veh ' + b.status + (eff !== 'rect' ? ' gp-media' : '') + (eff === 'symbol' ? ' gp-symmode' : '') + (b._id === P.sel ? ' sel' : '') + (inSel(b._id) ? ' multisel' : '') + (warn(b) || b._invalid ? ' invalid' : '') + (ctx ? ' dimctx' : '') });
                d.dataset.id = b._id;
                const warnTxt = !inside(b) ? '⚠ außerhalb' : (b._invalid ? '⚠ blockiert' : ((!heightOK(b) || !weightOK(b)) ? '⚠ Maß' : (doorHitsSpot(b) ? '⚠ Türaufschlag' : null)));
                const codeTxt = st.sym + ' ' + (b.type || 'Gefährt'), nameTxt = b.personName || b.label;
                // media background (behind the labels); symbol rotates with the block (facing)
                if (eff === 'symbol') {
                    if (b.plannerSymbol && b.plannerSymbol.indexOf('custom:') === 0) d.append(el('img', { class: 'gp-symimg', src: '/api/planner-icons/' + b.plannerSymbol.slice(7), alt: '', onerror: (e) => { e.target.remove(); d.insertBefore(symbolNode(b.type), d.firstChild); } }));
                    else d.append(symbolNode(b.plannerSymbol || b.type));
                }
                else if (eff === 'foto') { d.append(el('img', { class: 'gp-photo', src: b.photoUrl, alt: '' }), el('span', { class: 'gp-photoscrim' })); }
                if (eff !== 'rect') d.append(el('span', { class: 'gp-stbadge' }, st.sym)); // status glyph when the code chip is hidden (a11y; kept small on tiny blocks)
                if (b.rot) {
                    // rotated: one centred, upright label group (see positionBlock)
                    const grp = el('div', { class: 'gp-rlabel' }, el('span', { class: 'gp-rl-code' }, codeTxt), el('span', { class: 'gp-rl-name' }, nameTxt), el('span', { class: 'gp-rl-dim' }, dimText(b)));
                    if (warnTxt) grp.append(el('span', { class: 'gp-rl-warn' }, warnTxt));
                    d.append(grp);
                } else {
                    if (eff === 'rect') d.append(el('span', { class: 'gp-code' }, codeTxt));
                    if (warnTxt) d.append(el('span', { class: 'gp-warnb' }, warnTxt));
                    d.append(el('span', { class: 'gp-lab' }, el('span', { class: 'gp-who' }, nameTxt), el('span', { class: 'gp-dim' }, dimText(b))));
                }
                if (b.needsPower) d.append(el('span', { class: 'gp-power', title: 'Ladebedarf (Strom)' }, '⚡'));
                if (b._id === P.sel && interactive(b) && canManageNow) addHandles(d, b._id);
                positionBlock(d, b);
                layer.append(d);
            });
            P.excl.forEach((b) => {
                const ctx = !interactive(b);
                const cat = (EXCL[b.kind] && EXCL[b.kind].cat) || 'zone';
                const d = el('div', { class: 'gp-block excl gp-' + cat + ' ' + b.kind + (b.id === P.sel ? ' sel' : '') + (inSel(b.id) ? ' multisel' : '') + (ctx ? ' dimctx' : '') });
                d.dataset.id = b.id;
                // Zones show their measured area; structures show their type label.
                const lab = isZoneKind(b.kind) ? (b.label + ' · ' + (b.w * b.h).toFixed(1).replace('.', ',') + ' m²') : b.label;
                const szw = zoneSizeWarn(b); // GaVO min-size hint (soft, non-blocking)
                if (b.rot) d.append(el('div', { class: 'gp-rlabel' }, el('span', { class: 'gp-rl-name' }, lab), szw ? el('span', { class: 'gp-rl-warn' }, szw) : null));
                else d.append(el('span', { class: 'gp-lab' }, el('span', { class: 'gp-who' }, lab), szw ? el('span', { class: 'gp-warnb' }, szw) : null));
                if (b.id === P.sel && interactive(b) && canManageNow) addHandles(d, b.id);
                positionBlock(d, b); layer.append(d);
            });
            drawCalibFrame();
        }
        // Bauplan calibration frame: a draggable/resizable rectangle over the underlay
        // (plan mode only). drawFloor() re-renders the SVG image live; the frame lives in
        // the block layer and updates its own style during the gesture.
        function drawCalibFrame() {
            if (!(P.calib && P.plan && P.mode === 'plan' && canManageNow)) return;
            const CELL = P.CELL, pl = P.plan;
            const fr = el('div', { class: 'gp-calib' });
            const place = () => { fr.style.left = (pl.x * CELL) + 'px'; fr.style.top = (pl.y * CELL) + 'px'; fr.style.width = Math.max(6, pl.w * CELL) + 'px'; fr.style.height = Math.max(6, pl.h * CELL) + 'px'; };
            place();
            fr.append(el('span', { class: 'gp-calib-tag' }, 'Bauplan kalibrieren — ziehen · Ecke = Größe'));
            ['nw', 'ne', 'se', 'sw'].forEach((c) => { const h = el('div', { class: 'gp-calh gp-calh-' + c }); h.dataset.c = c; fr.append(h); });
            layer.append(fr);
            fr.addEventListener('pointerdown', (e) => {
                if (e.button !== 0) return;
                const handle = e.target.closest('.gp-calh'); e.preventDefault(); e.stopPropagation();
                const st = { mx: e.clientX, my: e.clientY, x: pl.x, y: pl.y, w: pl.w, h: pl.h, c: handle ? handle.dataset.c : null };
                const grip = handle || fr; try { grip.setPointerCapture(e.pointerId); } catch (er) { /* ignore */ }
                const mv = (ev) => {
                    const dx = (ev.clientX - st.mx) / CELL, dy = (ev.clientY - st.my) / CELL;
                    if (!st.c) { pl.x = st.x + dx; pl.y = st.y + dy; }
                    else { let x = st.x, y = st.y, w = st.w, h = st.h;
                        if (st.c.indexOf('w') >= 0) { x = st.x + dx; w = st.w - dx; }
                        if (st.c.indexOf('e') >= 0) { w = st.w + dx; }
                        if (st.c.indexOf('n') >= 0) { y = st.y + dy; h = st.h - dy; }
                        if (st.c.indexOf('s') >= 0) { h = st.h + dy; }
                        if (w > 0.3 && h > 0.3) { pl.x = x; pl.y = y; pl.w = w; pl.h = h; } }
                    place(); drawFloor();
                };
                const up = (ev) => { try { grip.releasePointerCapture(ev.pointerId); } catch (er) { /* ignore */ } window.removeEventListener('pointermove', mv); window.removeEventListener('pointerup', up); markDirty(); };
                window.addEventListener('pointermove', mv); window.addEventListener('pointerup', up);
            });
        }
        // Load an image file as the Bauplan underlay: downscale on a canvas and store a
        // JPEG data-URL small enough to fit inside the geometry blob (256 KB cap on the
        // whole JSON), then default its placement to the current floor bounding box.
        function loadBauplan(file) {
            if (!file) return;
            const rd = new FileReader();
            rd.onload = () => {
                const img = new Image();
                img.onload = () => {
                    const MAXPX = 1400; let w = img.naturalWidth || 1, h = img.naturalHeight || 1;
                    const sc = Math.min(1, MAXPX / Math.max(w, h)); w = Math.max(1, Math.round(w * sc)); h = Math.max(1, Math.round(h * sc));
                    const cv = document.createElement('canvas'); cv.width = w; cv.height = h;
                    const cx = cv.getContext('2d'); cx.fillStyle = '#fff'; cx.fillRect(0, 0, w, h); cx.drawImage(img, 0, 0, w, h);
                    let href = cv.toDataURL('image/jpeg', 0.72);
                    if (href.length > 200000) href = cv.toDataURL('image/jpeg', 0.5);
                    if (href.length > 230000) { toast('Bauplan zu groß — bitte ein kleineres/kontrastärmeres Bild wählen', 'error'); return; }
                    const bb = floorBB();
                    P.plan = { href, x: bb.minX, y: bb.minY, w: Math.max(1, bb.maxX - bb.minX), h: Math.max(1, bb.maxY - bb.minY), opacity: 0.55, hidden: false };
                    P.calib = true; markDirty(); draw(); renderRail(); toast('Bauplan geladen — jetzt kalibrieren (ziehen / Ecke)', 'ok');
                };
                img.onerror = () => toast('Bild konnte nicht gelesen werden', 'error');
                img.src = rd.result;
            };
            rd.onerror = () => toast('Datei konnte nicht gelesen werden', 'error');
            rd.readAsDataURL(file);
        }
        // ---- wall node-graph geometry ----
        function edgeVec(e) { const a = P.walls.nodes[e.a], b = P.walls.nodes[e.b]; const dx = b.x - a.x, dy = b.y - a.y; return { a, b, dx, dy, len: Math.hypot(dx, dy) }; }
        // Bezug (reference): the drawn line is the wall AXIS (centred, default), the INNER face
        // (thickness grows outward) or the OUTER face (grows inward). Each wall is shifted
        // PERPENDICULARLY by ±thick/2 (never moving the shared nodes → the plan can't skew); the
        // inner faces stay continuous at the drawn corners, so collision AND the enclosed area
        // land exactly on the drawn outline. Corners are filled by a joint at the averaged offset.
        const rectFrom = (a, b, kind, th) => { const dx = b.x - a.x, dy = b.y - a.y, len = Math.hypot(dx, dy); return len < 0.02 ? null : { kind, x: (a.x + b.x) / 2 - len / 2, y: (a.y + b.y) / 2 - th / 2, w: len, h: th, rot: Math.atan2(dy, dx) * 180 / Math.PI, _wall: true }; };
        function axisRects() { const out = []; for (const e of P.walls.edges) { const r = rectFrom(P.walls.nodes[e.a], P.walls.nodes[e.b], e.kind, e.thick || 0.24); if (r) out.push(r); } return out; }
        let _eoff = null, _eoffKey = null;
        function edgeOffs() { // per-edge perpendicular offset vector {ox,oy} (null in axis mode)
            if (P.wallRef === 'axis') return null;
            const key = P.wallRef + '#' + P.walls.nodes.map((n) => round2(n.x) + ',' + round2(n.y)).join(';') + '#' + P.walls.edges.map((e) => e.a + '-' + e.b + ':' + round2(e.thick || 0.24)).join(';');
            if (key === _eoffKey) return _eoff;
            _eoffKey = key; const enc = computeEnclosure(axisRects()); const dir = P.wallRef === 'inner' ? 1 : -1;
            const inSide = (sx, sy) => { if (!enc || !enc.lab) return false; const gx = Math.floor((sx - enc.minX) / enc.GS), gy = Math.floor((sy - enc.minY) / enc.GSy); if (gx < 0 || gy < 0 || gx >= enc.cols || gy >= enc.rows) return false; return enc.lab[gy * enc.cols + gx] >= 0; };
            _eoff = P.walls.edges.map((e) => { const v = edgeVec(e); if (v.len < 0.02 || !enc || !enc.lab) return { ox: 0, oy: 0 };
                const th = e.thick || 0.24, px = -v.dy / v.len, py = v.dx / v.len, mx = (v.a.x + v.b.x) / 2, my = (v.a.y + v.b.y) / 2;
                // probe both sides just past the wall face; cap the distance so a NARROW room can't
                // sample through the opposite wall, and retry closer when the first probe is ambiguous (S2-B).
                const probe = (d) => { const pl = inSide(mx + px * d, my + py * d), mi = inSide(mx - px * d, my - py * d); return (pl && !mi) ? -1 : (mi && !pl) ? 1 : 0; }; // s·perp = exterior; interior wall (both sides room) → 0
                let s = probe(th / 2 + Math.min(enc.GS * 1.3, 0.14)); if (s === 0) s = probe(th / 2 + enc.GS * 0.55);
                const mag = (th / 2) * s * dir; return { ox: px * mag, oy: py * mag }; });
            return _eoff;
        }
        function edgePts(e, ei) { const v = edgeVec(e), offs = edgeOffs(), o = offs ? offs[ei] : null; if (!o) return { a: v.a, b: v.b, dx: v.dx, dy: v.dy, len: v.len }; const a = { x: v.a.x + o.ox, y: v.a.y + o.oy }, b = { x: v.b.x + o.ox, y: v.b.y + o.oy }; return { a, b, dx: b.x - a.x, dy: b.y - a.y, len: v.len }; }
        // Corner point = where the two offset wall CENTRELINES actually cross (mitre) — so the
        // handle/joint sits exactly on the rendered corner. Display-only: walls still use the
        // perpendicular offset above (no node movement → no skew). 1 edge / junction → averaged.
        const lineX = (p, d, q, e) => { const den = d.x * e.y - d.y * e.x; if (Math.abs(den) < 1e-9) return null; const t = ((q.x - p.x) * e.y - (q.y - p.y) * e.x) / den; return { x: p.x + d.x * t, y: p.y + d.y * t }; };
        function nodeJoint(ni) {
            const offs = edgeOffs(), n = P.walls.nodes[ni]; if (!offs) return n;
            const inc = []; P.walls.edges.forEach((e, ei) => { if (e.a === ni || e.b === ni) inc.push(ei); });
            if (inc.length === 2) {
                const e1 = P.walls.edges[inc[0]], e2 = P.walls.edges[inc[1]], o1 = offs[inc[0]], o2 = offs[inc[1]];
                const f1 = e1.a === ni ? P.walls.nodes[e1.b] : P.walls.nodes[e1.a], f2 = e2.a === ni ? P.walls.nodes[e2.b] : P.walls.nodes[e2.a];
                const M = lineX({ x: n.x + o1.ox, y: n.y + o1.oy }, { x: f1.x - n.x, y: f1.y - n.y }, { x: n.x + o2.ox, y: n.y + o2.oy }, { x: f2.x - n.x, y: f2.y - n.y });
                if (M) return M;
            }
            let ox = 0, oy = 0, k = 0; inc.forEach((ei) => { ox += offs[ei].ox; oy += offs[ei].oy; k++; }); return k ? { x: n.x + ox / k, y: n.y + oy / k } : n;
        }
        // Each edge → a rotated rectangle (centre = midpoint, w = length, h = thickness), placed at
        // the Bezug-offset position, so walls plug into SAT collision AND the enclosure raster.
        function wallRects() { const out = []; P.walls.edges.forEach((e, ei) => { const vp = edgePts(e, ei); const r = rectFrom(vp.a, vp.b, e.kind, e.thick || 0.24); if (r) out.push(r); }); return out; }
        // Miter-joined wall polygons: per edge, the two boundary lines (offset-centreline ± t/2)
        // are intersected with the ANGULARLY-adjacent neighbour's boundary at each node, so
        // corners and T-/X-junctions merge cleanly (no gaps/steps/stubs) at any Bezug.
        let _wc = null, _wcKey = null;
        function wallCorners() {
            const key = P.wallRef + '#' + P.walls.nodes.map((n) => round2(n.x) + ',' + round2(n.y)).join(';') + '#' + P.walls.edges.map((e) => e.a + '-' + e.b + ':' + round2(e.thick || 0.24)).join(';');
            if (key === _wcKey) return _wc;
            _wcKey = key; const N = P.walls.nodes, E = P.walls.edges, offs = edgeOffs() || E.map(() => ({ ox: 0, oy: 0 }));
            const inc = N.map(() => []);
            E.forEach((e, ei) => { const a = N[e.a], b = N[e.b]; if (!a || !b) return; const dx = b.x - a.x, dy = b.y - a.y, L = Math.hypot(dx, dy) || 1;
                inc[e.a].push({ ei, dx: dx / L, dy: dy / L, ang: Math.atan2(dy, dx) }); inc[e.b].push({ ei, dx: -dx / L, dy: -dy / L, ang: Math.atan2(-dy, -dx) }); });
            inc.forEach((l) => l.sort((p, q) => p.ang - q.ang));
            const X = (px, py, dx, dy, qx, qy, ex, ey) => { const den = dx * ey - dy * ex; if (Math.abs(den) < 1e-9) return null; const t = ((qx - px) * ey - (qy - py) * ex) / den; return { x: px + dx * t, y: py + dy * t }; };
            const corner = (ni, ei) => {
                const list = inc[ni], k = list.findIndex((h) => h.ei === ei), he = list[k], n = list.length;
                const th = E[ei].thick || 0.24, o = offs[ei], base = { x: N[ni].x + o.ox, y: N[ni].y + o.oy }, nx = -he.dy, ny = he.dx, MIT = th * 6;
                const oth = he.ei === ei ? (E[ei].a === ni ? N[E[ei].b] : N[E[ei].a]) : null, eL = oth ? Math.hypot(oth.x - N[ni].x, oth.y - N[ni].y) : 1e9;
                const lp = { x: base.x + nx * th / 2, y: base.y + ny * th / 2 }, rp = { x: base.x - nx * th / 2, y: base.y - ny * th / 2 };
                let left = lp, right = rp;
                // Accept a mitre only if it neither exceeds the mitre limit NOR overshoots more than
                // half this edge's length along it — that stops spikes when two nodes sit close.
                const ok = (M, p0) => M && Math.hypot(M.x - N[ni].x, M.y - N[ni].y) <= MIT && Math.abs((M.x - p0.x) * he.dx + (M.y - p0.y) * he.dy) <= eL * 0.5;
                if (n >= 2) {
                    const nb = list[(k + 1) % n]; if (nb.ei !== ei) { const thN = E[nb.ei].thick || 0.24, oN = offs[nb.ei], bx = N[ni].x + oN.ox, by = N[ni].y + oN.oy, nnx = -nb.dy, nny = nb.dx;
                        const M = X(lp.x, lp.y, he.dx, he.dy, bx - nnx * thN / 2, by - nny * thN / 2, nb.dx, nb.dy); if (ok(M, lp)) left = M; }
                    const pb = list[(k - 1 + n) % n]; if (pb.ei !== ei) { const thP = E[pb.ei].thick || 0.24, oP = offs[pb.ei], bx = N[ni].x + oP.ox, by = N[ni].y + oP.oy, ppx = -pb.dy, ppy = pb.dx;
                        const M = X(rp.x, rp.y, he.dx, he.dy, bx + ppx * thP / 2, by + ppy * thP / 2, pb.dx, pb.dy); if (ok(M, rp)) right = M; }
                }
                return { left, right };
            };
            _wc = E.map((e, ei) => { const a = N[e.a], b = N[e.b]; if (!a || !b) return null; const cA = corner(e.a, ei), cB = corner(e.b, ei); return { aL: cA.left, aR: cA.right, bL: cB.right, bR: cB.left }; });
            return _wc;
        }
        // Point on the RENDERED (mitre-offset) centre line of edge ei at drawn-edge param oc, plus the
        // unit wall normal there — so door sweeps / opening grips sit exactly under the drawn opening (C3).
        function offCenterPt(ei, oc) {
            const e = P.walls.edges[ei], v0 = edgeVec(e), wc = (wallCorners() || [])[ei];
            const wx = v0.a.x + v0.dx * oc, wy = v0.a.y + v0.dy * oc;
            if (!wc) { const L = v0.len || 1; return { x: wx, y: wy, nx: -v0.dy / L, ny: v0.dx / L }; }
            const oa = { x: (wc.aL.x + wc.aR.x) / 2, y: (wc.aL.y + wc.aR.y) / 2 }, ob = { x: (wc.bL.x + wc.bR.x) / 2, y: (wc.bL.y + wc.bR.y) / 2 };
            const dx = ob.x - oa.x, dy = ob.y - oa.y, L2 = dx * dx + dy * dy || 1, L = Math.sqrt(L2);
            const cc = Math.max(0, Math.min(1, ((wx - oa.x) * dx + (wy - oa.y) * dy) / L2));
            return { x: oa.x + dx * cc, y: oa.y + dy * cc, nx: -dy / L, ny: dx / L };
        }
        // Lichtes (clear/inner) length of an edge = axis length minus half the thickness of the
        // walls meeting at each end (a room corner eats thick/2 of clear span on that side).
        function nodePerpHalf(ni, exceptEi) { let mx = 0; P.walls.edges.forEach((x, xi) => { if (xi === exceptEi) return; if (x.a === ni || x.b === ni) mx = Math.max(mx, x.thick || 0.24); }); return mx / 2; }
        // Displayed dimension depends on the Bezug: inner ⇒ the drawn length is already the clear
        // span; axis ⇒ minus ½ wall per corner; outer ⇒ minus a full wall per corner.
        function labelLen(ei) { const e = P.walls.edges[ei], v = edgeVec(e); if (P.wallRef === 'inner') return v.len; if (P.wallRef === 'outer') return Math.max(0, v.len - 2 * nodePerpHalf(e.a, ei) - 2 * nodePerpHalf(e.b, ei)); return Math.max(0, v.len - nodePerpHalf(e.a, ei) - nodePerpHalf(e.b, ei)); }
        function nodeAt(p, r) { let bi = -1, bd = r; P.walls.nodes.forEach((n, i) => { const d = Math.hypot(n.x - p.x, n.y - p.y); if (d < bd) { bd = d; bi = i; } }); return bi; }
        // Node hit-test against the DISPLAYED handle position (the offset corner) so grabbing the
        // dot the user actually sees works in Innen/Außen mode; equals nodeAt in Achse mode.
        function nodeJointAt(p, r) { let bi = -1, bd = r; for (let i = 0; i < P.walls.nodes.length; i++) { const jp = nodeJoint(i); const d = Math.hypot(jp.x - p.x, jp.y - p.y); if (d < bd) { bd = d; bi = i; } } return bi; }
        function edgeAt(p, r) { let best = null; const offs = edgeOffs(); P.walls.edges.forEach((e, i) => { const v = edgeVec(e); if (v.len < 0.02) return; let t = ((p.x - v.a.x) * v.dx + (p.y - v.a.y) * v.dy) / (v.len * v.len); t = Math.max(0, Math.min(1, t)); const x = v.a.x + t * v.dx, y = v.a.y + t * v.dy; let d = Math.hypot(p.x - x, p.y - y); if (offs && offs[i]) { const d2 = Math.hypot(p.x - (x + offs[i].ox), p.y - (y + offs[i].oy)); if (d2 < d) d = d2; } /* also pick the rendered (offset) wall — keep t/x/y on the drawn line for attach */ if (d < r && (!best || d < best.dist)) best = { i, t, x, y, dist: d }; }); return best; }
        function addNode(x, y) { for (let i = 0; i < P.walls.nodes.length; i++) if (Math.hypot(P.walls.nodes[i].x - x, P.walls.nodes[i].y - y) < 0.05) return i; P.walls.nodes.push({ x: round2(x), y: round2(y) }); return P.walls.nodes.length - 1; }
        function addEdge(a, b, kind) { if (a === b) return; if (P.walls.edges.some((e) => (e.a === a && e.b === b) || (e.a === b && e.b === a))) return; P.walls.edges.push({ a, b, kind, thick: P.wallThick || (EXCL[kind] && EXCL[kind].h) || 0.24, ops: [] }); }
        function setEdgeThickness(ei, cm) { const e = P.walls.edges[ei]; if (!e) return; const t = Math.max(0.05, Math.min(1, cm / 100)); e.thick = round2(t); refreshFloorFromWalls(); pushUndo(); markDirty(); equalizePadding(); toast('Wandstärke ' + Math.round(t * 100) + ' cm', 'ok'); }
        // Break an edge at parameter t: insert a shared node and replace it with two edges,
        // re-parameterising any openings onto whichever half they fall in (Anbau / Vertex).
        function splitEdgeAt(ei, t) {
            const e = P.walls.edges[ei], v = edgeVec(e), ni = addNode(v.a.x + t * v.dx, v.a.y + t * v.dy);
            if (ni === e.a || ni === e.b) return ni;
            const opsA = [], opsB = []; (e.ops || []).forEach((o) => { const base = { w: o.w, kind: o.kind, side: o.side, hinge: o.hinge, frame: o.frame }; if (o.c <= t) opsA.push({ ...base, c: t < 1e-6 ? 0 : o.c / t }); else opsB.push({ ...base, c: (o.c - t) / (1 - t) }); }); // keep door side/hinge/frame across a split (F2)
            P.walls.edges.splice(ei, 1, { a: e.a, b: ni, kind: e.kind, thick: e.thick, ops: opsA }, { a: ni, b: e.b, kind: e.kind, thick: e.thick, ops: opsB });
            return ni;
        }
        // "Punkt löschen": if the node joins exactly two segments A–B and B–C, DISSOLVE it —
        // merge them into one continuous edge A–C (re-parameterising openings by path length).
        // Any other degree just drops the node's incident edges. pruneNodes() cleans up.
        function deleteNode(ni) {
            const inc = []; P.walls.edges.forEach((e, i) => { if (e.a === ni || e.b === ni) inc.push(i); });
            if (inc.length === 2) {
                const e1 = P.walls.edges[inc[0]], e2 = P.walls.edges[inc[1]];
                const A = e1.a === ni ? e1.b : e1.a, C = e2.a === ni ? e2.b : e2.a;
                const dup = P.walls.edges.some((e, i) => i !== inc[0] && i !== inc[1] && ((e.a === A && e.b === C) || (e.a === C && e.b === A)));
                const hi = Math.max(inc[0], inc[1]), lo = Math.min(inc[0], inc[1]);
                if (A !== C && !dup) {
                    const nA = P.walls.nodes[A], nB = P.walls.nodes[ni], nC = P.walls.nodes[C];
                    const lAB = Math.hypot(nB.x - nA.x, nB.y - nA.y), lBC = Math.hypot(nC.x - nB.x, nC.y - nB.y), tot = lAB + lBC || 1;
                    const ops = [];
                    // Keep door side/hinge/frame; when the merge reverses an edge's direction, flip
                    // the swing side + hinge end so the door still points the same way in the world (F3).
                    (e1.ops || []).forEach((o) => { const rev = e1.a !== A, fromA = (rev ? 1 - o.c : o.c) * lAB; ops.push({ c: Math.max(0, Math.min(1, fromA / tot)), w: o.w, kind: o.kind, side: rev ? -(o.side || 1) : o.side, hinge: rev ? (o.hinge ? 0 : 1) : o.hinge, frame: o.frame }); });
                    (e2.ops || []).forEach((o) => { const rev = e2.a !== ni, fromB = (rev ? 1 - o.c : o.c) * lBC; ops.push({ c: Math.max(0, Math.min(1, (lAB + fromB) / tot)), w: o.w, kind: o.kind, side: rev ? -(o.side || 1) : o.side, hinge: rev ? (o.hinge ? 0 : 1) : o.hinge, frame: o.frame }); });
                    P.walls.edges.splice(hi, 1); P.walls.edges.splice(lo, 1);
                    P.walls.edges.push({ a: A, b: C, kind: e1.kind, thick: e1.thick, ops });
                } else { P.walls.edges.splice(hi, 1); P.walls.edges.splice(lo, 1); }
            } else {
                P.walls.edges = P.walls.edges.filter((e) => e.a !== ni && e.b !== ni);
            }
            pruneNodes();
        }
        function pruneNodes() { const used = new Set(); P.walls.edges.forEach((e) => { used.add(e.a); used.add(e.b); }); const keep = [], remap = {}; P.walls.nodes.forEach((n, i) => { if (used.has(i)) { remap[i] = keep.length; keep.push(n); } }); P.walls.nodes = keep; P.walls.edges.forEach((e) => { e.a = remap[e.a]; e.b = remap[e.b]; }); }
        function deleteEdge(ei) { P.walls.edges.splice(ei, 1); pruneNodes(); }
        function wallsBBox() { let a = Infinity, b = Infinity, c = -Infinity, d = -Infinity; for (const n of P.walls.nodes) { if (n.x < a) a = n.x; if (n.y < b) b = n.y; if (n.x > c) c = n.x; if (n.y > d) d = n.y; } return { minX: a, minY: b, maxX: c, maxY: d }; }
        // Chosen model "Wände sind die Grenze": after any wall edit the hall floor tracks the
        // wall-enclosed outline (so placement/metrics use it); falls back to the wall bbox
        // while the ring is still open. Also grows the canvas to fit the drawing.
        function refreshFloorFromWalls() {
            _encKey = null;
            if (P.walls.nodes.length) { const bb = wallsBBox(); P.Wm = Math.max(P.Wm, Math.ceil(bb.maxX + 1.5)); P.Hm = Math.max(P.Hm, Math.ceil(bb.maxY + 1.5)); }
            const poly = traceEnclosurePoly(encNow());
            if (poly && poly.length >= 3) P.floor = poly;
            else if (P.walls.nodes.length >= 2) { const bb = wallsBBox(); const pad = 0.1; P.floor = [[bb.minX - pad, bb.minY - pad], [bb.maxX + pad, bb.minY - pad], [bb.maxX + pad, bb.maxY + pad], [bb.minX - pad, bb.maxY + pad]]; }
        }
        function encNow() { const wb = wallBlocks(); return wb.length ? computeEnclosure(wb) : null; }
        // Set an edge's exact length by moving its freer end along the drawing direction
        // (the end with fewer connections stays put if the other is a shared junction).
        function setEdgeLength(ei, L) {
            const e = P.walls.edges[ei]; if (!e) return; const v = edgeVec(e); if (v.len < 1e-6 || !(L > 0.05)) return;
            const deg = (n) => P.walls.edges.reduce((s, x) => s + (x.a === n || x.b === n ? 1 : 0), 0);
            let mv, fx; if (deg(e.b) <= deg(e.a)) { mv = e.b; fx = e.a; } else { mv = e.a; fx = e.b; }
            const f = P.walls.nodes[fx], m = P.walls.nodes[mv]; let dx = m.x - f.x, dy = m.y - f.y; const d = Math.hypot(dx, dy) || 1; dx /= d; dy /= d;
            P.walls.nodes[mv] = { x: round2(f.x + dx * L), y: round2(f.y + dy * L) };
            refreshFloorFromWalls(); pushUndo(); markDirty(); layout(); toast('Länge ' + L.toFixed(2).replace('.', ',') + ' m', 'ok');
        }
        // Openings: an opening lives on edge ei at index oi as {c(param),w(m),kind}.
        function setOpeningWidth(ei, oi, w) {
            const e = P.walls.edges[ei]; if (!e || !(e.ops || [])[oi]) return; const v = edgeVec(e);
            w = Math.max(0.2, Math.min(v.len * 0.98, w)); const o = e.ops[oi]; o.w = round2(w);
            const hp = (w / v.len) / 2; o.c = Math.max(hp, Math.min(1 - hp, o.c));
            pushUndo(); markDirty(); layout(); toast('Öffnung ' + w.toFixed(2).replace('.', ',') + ' m', 'ok');
        }
        function openingAt(p, r) {
            let best = null; const offs = edgeOffs();
            P.walls.edges.forEach((e, ei) => { const v = edgeVec(e); if (v.len < 0.02) return; const o0 = (offs && offs[ei]) || { ox: 0, oy: 0 }; (e.ops || []).forEach((o, oi) => {
                const t = ((p.x - v.a.x) * v.dx + (p.y - v.a.y) * v.dy) / (v.len * v.len), along = t * v.len, c = o.c * v.len, half = o.w / 2;
                if (along < c - half - r || along > c + half + r) return;
                // measure perpendicular distance to the RENDERED (offset) opening, not the drawn line
                const px = v.a.x + t * v.dx + o0.ox, py = v.a.y + t * v.dy + o0.oy, perp = Math.hypot(p.x - px, p.y - py);
                if (perp > (e.thick || 0.24) / 2 + r) return;
                if (!best || perp < best.perp) best = { ei, oi, perp };
            }); });
            return best;
        }
        // Türanschlag: currently-selected door opening (or null), + flip helpers.
        function doorSel() { const ss = P.structSel; if (ss && ss.type === 'opening') { const e = P.walls.edges[ss.ei], o = e && (e.ops || [])[ss.oi]; if (o && o.kind === 'door') return { e, o }; } return null; }
        function flipDoorSide() { const d = doorSel(); if (!d) return; d.o.side = (d.o.side || 1) * -1; pushUndo(); markDirty(); draw(); toast('Tür gespiegelt (Öffnungsrichtung)'); }
        function swapDoorHinge() { const d = doorSel(); if (!d) return; d.o.hinge = d.o.hinge ? 0 : 1; pushUndo(); markDirty(); draw(); toast('Anschlag gewechselt (links/rechts)'); }
        function openingHandleAt(p, ei, oi, r) {
            const e = P.walls.edges[ei]; if (!e || !(e.ops || [])[oi]) return null; const v = edgeVec(e), o = e.ops[oi], hp = (o.w / v.len) / 2;
            const pLo = offCenterPt(ei, o.c - hp), pHi = offCenterPt(ei, o.c + hp); // grips on the mitre-offset centre line (F6/C3)
            if (Math.hypot(p.x - pLo.x, p.y - pLo.y) < r) return 'lo';
            if (Math.hypot(p.x - pHi.x, p.y - pHi.y) < r) return 'hi';
            return null;
        }
        // Infinite-canvas support: shift ALL geometry (walls/zones/spots/floor/plan) by dx,dy.
        function shiftWorld(dx, dy) {
            if (!dx && !dy) return;
            P.walls.nodes.forEach((n) => { n.x = round2(n.x + dx); n.y = round2(n.y + dy); });
            P.excl.forEach((e) => { e.x += dx; e.y += dy; });
            P.spots.forEach((s) => { s.x += dx; s.y += dy; s._dirty = true; });
            P.floor = P.floor.map(([x, y]) => [round2(x + dx), round2(y + dy)]);
            if (P.plan) { P.plan.x += dx; P.plan.y += dy; }
        }
        // Grow the canvas by l/t/r/b metres, keeping content visually anchored (scroll comp).
        // The scale is a fixed base now, so growth simply extends the plane (no rescale).
        function expandCanvas(l, t, r, b) {
            if (!(l || t || r || b)) return false;
            const sl = planWrap.scrollLeft, st = planWrap.scrollTop;
            if (l || t) shiftWorld(l, t);
            P.Wm += l + r; P.Hm += t + b; _encKey = null;
            layout();
            planWrap.scrollLeft = sl + l * P.CELL; planWrap.scrollTop = st + t * P.CELL;
            return true;
        }
        function maybeExpand(p) {
            const PAD = 1.2;
            const l = p.x < PAD ? Math.ceil(PAD - p.x) : 0, t = p.y < PAD ? Math.ceil(PAD - p.y) : 0;
            const r = p.x > P.Wm - PAD ? Math.ceil(p.x + PAD - P.Wm) : 0, b = p.y > P.Hm - PAD ? Math.ceil(p.y + PAD - P.Hm) : 0;
            return (l || t || r || b) ? expandCanvas(l, t, r, b) : false;
        }
        // AABB of everything that actually exists (walls, zones, spots, underlay).
        function contentBBox() {
            let a = Infinity, b = Infinity, c = -Infinity, d = -Infinity, any = false;
            const acc = (x, y) => { any = true; if (x < a) a = x; if (y < b) b = y; if (x > c) c = x; if (y > d) d = y; };
            P.walls.nodes.forEach((n) => acc(n.x, n.y));
            P.excl.concat(P.spots).forEach((e) => { const h = halfAABB(e), cx = e.x + e.w / 2, cy = e.y + e.h / 2; acc(cx - h.hx, cy - h.hy); acc(cx + h.hx, cy + h.hy); });
            if (P.plan) { acc(P.plan.x, P.plan.y); acc(P.plan.x + P.plan.w, P.plan.y + P.plan.h); }
            return any ? { minX: a, minY: b, maxX: c, maxY: d } : null;
        }
        // Re-hug the canvas around the real content with EXACTLY equal padding on all four
        // sides (shifts content so left/top = PAD; sets Wm/Hm so right/bottom = PAD too).
        // Keeps the current scale — content stays anchored via scroll compensation.
        const CANVAS_PAD = 2;
        function equalizePadding() {
            const MINW = 8, MINH = 6, bb = contentBBox();
            if (!bb) { P.Wm = 14; P.Hm = 9; _encKey = null; layout(); return; }
            const dx = round2(CANVAS_PAD - bb.minX), dy = round2(CANVAS_PAD - bb.minY);
            const sl = planWrap.scrollLeft, st = planWrap.scrollTop;
            if (Math.abs(dx) > 0.01 || Math.abs(dy) > 0.01) shiftWorld(dx, dy);
            P.Wm = Math.max(MINW, Math.ceil((bb.maxX - bb.minX) + 2 * CANVAS_PAD));
            P.Hm = Math.max(MINH, Math.ceil((bb.maxY - bb.minY) + 2 * CANVAS_PAD));
            _encKey = null; layout();
            planWrap.scrollLeft = Math.max(0, sl + dx * P.CELL); planWrap.scrollTop = Math.max(0, st + dy * P.CELL);
        }
        // Zoom-to-fit: hug the content AABB with a small RELATIVE margin (≈5 % of the larger
        // side, clamped 0.4–1.5 m — not a fixed 2 m that dwarfs a small plan), refit the scale so
        // it fills the viewport, and let layout() centre it. This is the Einpassen / fullscreen fit.
        function fitView() {
            const bb = contentBBox();
            if (!bb) { P.base = null; P.zoom = 1; equalizePadding(); return; }
            const w = bb.maxX - bb.minX, h = bb.maxY - bb.minY;
            const pad = Math.min(1.5, Math.max(0.4, Math.max(w, h) * 0.05));
            const dx = round2(pad - bb.minX), dy = round2(pad - bb.minY);
            if (Math.abs(dx) > 0.01 || Math.abs(dy) > 0.01) shiftWorld(dx, dy);
            P.Wm = Math.max(8, round2(w + 2 * pad)); P.Hm = Math.max(6, round2(h + 2 * pad));
            P.base = null; P.zoom = 1; _encKey = null; _eoffKey = null; _wcKey = null;
            layout();
        }

        // ---- automatic Parkfläche: the area ENCLOSED by the drawn walls ----
        // Walls (+ openings) are the physical building boundary. We rasterise, flood-fill
        // the exterior from the grid border, and whatever open cell the exterior can't
        // reach is enclosed interior = the usable parking area. "Umriss übernehmen" then
        // traces that enclosure's OUTER face and adopts it as the hall floor polygon.
        // Enclosure boundary = drawn wall segments (graph) + any legacy excl walls/openings.
        function wallBlocks() { return wallRects().concat(P.excl.filter((e) => isWallKind(e.kind) || isOpenKind(e.kind))); }
        // Pure geometry lives in geometry.js (window.PG) so it is unit-tested directly (AR2).
        const pointInPoly = PG.pointInPoly;
        // Planar face decomposition → exact interior polygon + area of EVERY enclosed room, honouring
        // the Bezug via edgeOffs; delegated to the tested PG.roomAreas.
        function roomFaces() { return PG.roomAreas(P.walls.nodes, P.walls.edges, P.wallRef); }
        let _enc = null, _encKey = null;
        function enclosure() {
            const wb = wallBlocks();
            const key = P.wallRef + '#' + wb.map((e) => e.kind + ':' + round2(e.x) + ',' + round2(e.y) + ',' + round2(e.w) + ',' + round2(e.h) + ',' + Math.round(e.rot || 0)).join('|') + '#' + JSON.stringify(P.floor);
            if (key === _encKey) return _enc;
            _encKey = key; _enc = wb.length ? computeEnclosure(wb) : null;
            // Replace each room's quantised raster area with the exact analytic face area (C1), matched
            // by the raster centroid; subtract interior Stützen inside that room (S1-A). A face is only
            // trusted when it's within ~50 % of the raster value (guards against degenerate stub faces);
            // otherwise that room keeps its raster area.
            if (_enc && _enc.rooms && _enc.rooms.length) {
                const faces = roomFaces(); let tot = 0;
                for (const rm of _enc.rooms) {
                    const f = faces.find((ff) => pointInPoly(rm.cx, rm.cy, ff.ring));
                    if (f && f.area > 0.3 && Math.abs(f.area - rm.area) < rm.area * 0.5 + 2) {
                        let a = f.area;
                        for (const b of P.excl) if (isWallKind(b.kind) && pointInPoly(b.x + b.w / 2, b.y + b.h / 2, f.ring)) a -= Math.abs(b.w * b.h);
                        rm.area = Math.max(0.3, a);
                    }
                    tot += rm.area;
                }
                _enc.area = tot;
            }
            return _enc;
        }
        function computeEnclosure(wb) {
            let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
            const acc = (x, y) => { if (x < minX) minX = x; if (y < minY) minY = y; if (x > maxX) maxX = x; if (y > maxY) maxY = y; };
            P.floor.forEach(([x, y]) => acc(x, y));
            wb.forEach((e) => { const h = halfAABB(e), cx = e.x + e.w / 2, cy = e.y + e.h / 2; acc(cx - h.hx, cy - h.hy); acc(cx + h.hx, cy + h.hy); });
            if (!(maxX > minX && maxY > minY)) return null;
            const pad = 0.6; minX -= pad; minY -= pad; maxX += pad; maxY += pad;
            const boxW = maxX - minX, boxH = maxY - minY;
            const cols = Math.max(3, Math.min(320, Math.round(boxW / Math.max(0.12, Math.sqrt(boxW * boxH / 18000)))));
            const rows = Math.max(3, Math.min(320, Math.round(cols * boxH / boxW)));
            const GS = boxW / cols, GSy = boxH / rows, N = cols * rows, blocked = new Uint8Array(N);
            for (const e of wb) {
                const rad = (e.rot || 0) * Math.PI / 180, cos = Math.cos(rad), sin = Math.sin(rad);
                const cx = e.x + e.w / 2, cy = e.y + e.h / 2, hh = halfAABB(e);
                const gx0 = Math.max(0, Math.floor((cx - hh.hx - minX) / GS) - 1), gx1 = Math.min(cols - 1, Math.ceil((cx + hh.hx - minX) / GS) + 1);
                const gy0 = Math.max(0, Math.floor((cy - hh.hy - minY) / GSy) - 1), gy1 = Math.min(rows - 1, Math.ceil((cy + hh.hy - minY) / GSy) + 1);
                // Conservative: block a cell whose centre is within HALF a cell of the wall — a wall
                // thinner than the grid (GS > thickness on large plans) then still blocks a full band
                // instead of letting the exterior flood leak straight through (F1).
                for (let gy = gy0; gy <= gy1; gy++) for (let gx = gx0; gx <= gx1; gx++) {
                    const px = minX + (gx + 0.5) * GS, py = minY + (gy + 0.5) * GSy, dx = px - cx, dy = py - cy;
                    if (Math.abs(dx * cos + dy * sin) <= e.w / 2 + GS / 2 && Math.abs(-dx * sin + dy * cos) <= e.h / 2 + GSy / 2) blocked[gy * cols + gx] = 1;
                }
            }
            const seen = new Uint8Array(N), stack = [];
            const push = (i) => { if (!seen[i] && !blocked[i]) { seen[i] = 1; stack.push(i); } };
            for (let gx = 0; gx < cols; gx++) { push(gx); push((rows - 1) * cols + gx); }
            for (let gy = 0; gy < rows; gy++) { push(gy * cols); push(gy * cols + cols - 1); }
            while (stack.length) { const i = stack.pop(), gx = i % cols, gy = (i - gx) / cols; if (gx > 0) push(i - 1); if (gx < cols - 1) push(i + 1); if (gy > 0) push(i - cols); if (gy < rows - 1) push(i + cols); }
            const interior = new Uint8Array(N); let count = 0, sx = 0, sy = 0;
            for (let i = 0; i < N; i++) if (!blocked[i] && !seen[i]) { interior[i] = 1; count++; const gx = i % cols, gy = (i - gx) / cols; sx += minX + (gx + 0.5) * GS; sy += minY + (gy + 0.5) * GSy; }
            if (!count) return null;
            const rects = [];
            for (let gy = 0; gy < rows; gy++) { let run = -1; for (let gx = 0; gx <= cols; gx++) { const on = gx < cols && interior[gy * cols + gx]; if (on && run < 0) run = gx; else if (!on && run >= 0) { rects.push({ x: minX + run * GS, y: minY + gy * GSy, w: (gx - run) * GS, h: GSy }); run = -1; } } }
            // Per-room m²: connected components of the interior grid, each with area + centroid.
            const lab = new Int32Array(N).fill(-1); const rooms = []; let cur = 0;
            for (let s = 0; s < N; s++) { if (!interior[s] || lab[s] >= 0) continue;
                const st = [s]; lab[s] = cur; let n = 0, rx = 0, ry = 0;
                while (st.length) { const i = st.pop(), gx = i % cols, gy = (i - gx) / cols; n++; rx += minX + (gx + 0.5) * GS; ry += minY + (gy + 0.5) * GSy;
                    if (gx > 0 && interior[i - 1] && lab[i - 1] < 0) { lab[i - 1] = cur; st.push(i - 1); }
                    if (gx < cols - 1 && interior[i + 1] && lab[i + 1] < 0) { lab[i + 1] = cur; st.push(i + 1); }
                    if (gy > 0 && interior[i - cols] && lab[i - cols] < 0) { lab[i - cols] = cur; st.push(i - cols); }
                    if (gy < rows - 1 && interior[i + cols] && lab[i + cols] < 0) { lab[i + cols] = cur; st.push(i + cols); } }
                const a = n * GS * GSy; if (a > 0.5) rooms.push({ id: cur, area: a, cx: rx / n, cy: ry / n }); cur++;
            }
            rooms.sort((p, q) => q.area - p.area);
            return { area: count * GS * GSy, rects, rooms, lab, cx: sx / count, cy: sy / count, cols, rows, minX, minY, GS, GSy, seen };
        }
        // Trace the outer face of the enclosed building (region = everything the exterior
        // flood can't reach = interior + surrounding walls) into a simplified metre polygon.
        // Returns null if there's no closed region. Shared by the auto floor-tracking and
        // the explicit "Umriss übernehmen" button.
        function traceEnclosurePoly(enc) {
            if (!enc) return null;
            const { cols, rows, minX, minY, GS, GSy, seen } = enc;
            const region = new Uint8Array(cols * rows); for (let i = 0; i < region.length; i++) region[i] = seen[i] ? 0 : 1;
            const lab = new Int32Array(region.length).fill(-1); let best = -1, bestN = 0, cur = 0;
            for (let s = 0; s < region.length; s++) { if (!region[s] || lab[s] >= 0) continue; const st = [s]; lab[s] = cur; let n = 0; while (st.length) { const i = st.pop(); n++; const gx = i % cols, gy = (i - gx) / cols; const nb = []; if (gx > 0) nb.push(i - 1); if (gx < cols - 1) nb.push(i + 1); if (gy > 0) nb.push(i - cols); if (gy < rows - 1) nb.push(i + cols); for (const j of nb) if (region[j] && lab[j] < 0) { lab[j] = cur; st.push(j); } } if (n > bestN) { bestN = n; best = cur; } cur++; }
            if (best < 0) return null;
            const inC = (gx, gy) => gx >= 0 && gy >= 0 && gx < cols && gy < rows && lab[gy * cols + gx] === best;
            const edges = new Map(), K = (x, y) => x + ',' + y;
            for (let gy = 0; gy < rows; gy++) for (let gx = 0; gx < cols; gx++) { if (lab[gy * cols + gx] !== best) continue;
                if (!inC(gx, gy - 1)) edges.set(K(gx, gy), [gx + 1, gy]);
                if (!inC(gx + 1, gy)) edges.set(K(gx + 1, gy), [gx + 1, gy + 1]);
                if (!inC(gx, gy + 1)) edges.set(K(gx + 1, gy + 1), [gx, gy + 1]);
                if (!inC(gx - 1, gy)) edges.set(K(gx, gy + 1), [gx, gy]);
            }
            if (!edges.size) return null;
            const first = edges.keys().next().value, start = first.split(',').map(Number);
            const loop = [start]; let cornKey = K(start[0], start[1]), guard = edges.size + 4;
            while (guard-- > 0) { const nx = edges.get(cornKey); if (!nx) break; if (nx[0] === start[0] && nx[1] === start[1]) break; loop.push(nx); cornKey = K(nx[0], nx[1]); }
            const pts = loop.filter((p, i) => { const a = loop[(i - 1 + loop.length) % loop.length], b = loop[(i + 1) % loop.length]; return (p[0] - a[0]) * (b[1] - a[1]) !== (p[1] - a[1]) * (b[0] - a[0]); });
            if (pts.length < 3) return null;
            let poly = pts.map(([gx, gy]) => [Math.round((minX + gx * GS) * 100) / 100, Math.round((minY + gy * GSy) * 100) / 100]);
            poly = rdp(poly, 0.3); if (poly.length < 3) return null;
            if (Math.abs(polyArea(poly)) < enc.area * 0.5) return null; // partial/unclosed trace → reject
            return poly;
        }
        // Explicit "adopt the wall outline as the hall floor" (also used automatically after edits).
        function deriveFloorFromWalls() {
            const poly = traceEnclosurePoly(encNow());
            if (!poly) { toast('Erst Außenwände zu einem geschlossenen Ring zeichnen', 'warn'); return; }
            P.floor = poly; P.shape = 'rect'; refit(); hideLen(); pushUndo(); markDirty(); layout();
            toast('Umriss aus Wänden übernommen · ' + Math.round(polyArea(P.floor)) + ' m²', 'ok');
        }
        // Ramer–Douglas–Peucker polygon simplification (closed ring).
        function rdp(poly, eps) {
            const d2 = (p, a, b) => { const dx = b[0] - a[0], dy = b[1] - a[1], L = dx * dx + dy * dy; if (!L) return Math.hypot(p[0] - a[0], p[1] - a[1]); let t = ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / L; t = Math.max(0, Math.min(1, t)); return Math.hypot(p[0] - (a[0] + t * dx), p[1] - (a[1] + t * dy)); };
            const simp = (s, e, path) => { let idx = -1, mx = eps; for (let i = s + 1; i < e; i++) { const d = d2(path[i], path[s], path[e]); if (d > mx) { mx = d; idx = i; } } if (idx < 0) return [path[s]]; return simp(s, idx, path).concat(simp(idx, e, path)); };
            const open = poly.concat([poly[0]]); return simp(0, open.length - 1, open);
        }
        // Which enclosed room contains a world point (for the click-to-show readout)?
        function roomAt(p) {
            const enc = P.autoArea ? enclosure() : null; if (!enc || !enc.lab) return null;
            const gx = Math.floor((p.x - enc.minX) / enc.GS), gy = Math.floor((p.y - enc.minY) / enc.GSy);
            if (gx < 0 || gy < 0 || gx >= enc.cols || gy >= enc.rows) return null;
            const id = enc.lab[gy * enc.cols + gx]; if (id < 0) return null;
            return (enc.rooms || []).find((r) => r.id === id) || null;
        }
        function drawEnclosure() {
            if (!P.autoArea) return;
            const enc = enclosure(); if (!enc || enc.area < 0.3) return;
            const CELL = P.CELL, g = svgEl('g', { class: 'gp-parkg' });
            for (const r of enc.rects) g.append(svgEl('rect', { x: r.x * CELL, y: r.y * CELL, width: r.w * CELL, height: r.h * CELL, class: 'gp-parkfill' }));
            svg.append(g);
            // One m² label PER enclosed room (single room → "Parkfläche", mehrere → "Raum N").
            const rooms = (enc.rooms && enc.rooms.length) ? enc.rooms : [{ area: enc.area, cx: enc.cx, cy: enc.cy }];
            rooms.forEach((rm, i) => { const t = svgEl('text', { x: rm.cx * CELL, y: rm.cy * CELL, 'text-anchor': 'middle', class: 'gp-parklab' }); t.textContent = (rooms.length > 1 ? 'Raum ' + (i + 1) + ' · ' : 'Parkfläche ') + rm.area.toFixed(1).replace('.', ',') + ' m²'; svg.append(t); });
        }
        // Draw one opening in the (already cut-out) gap: reveal jambs at both ends + a kind
        // symbol (window glass / door leaf+swing / gate bar), plus handles when selected.
        function drawOpening(g, v, e, oi, CELL, cc) {
            const o = e.ops[oi], col = OPENCOL[o.kind] || '#4ea8f5', hp = (o.w / v.len) / 2, oc = (cc != null ? cc : o.c); // cc = centre mapped onto the offset centre line (S3-D)
            const lo = Math.max(0, oc - hp), hi = Math.min(1, oc + hp);
            const th = Math.max(2, (e.thick || 0.24) * CELL), h = th / 2;
            const P0 = (u) => [(v.a.x + v.dx * u) * CELL, (v.a.y + v.dy * u) * CELL];
            let nx = -v.dy, ny = v.dx; const nl = Math.hypot(nx, ny) || 1; nx /= nl; ny /= nl;
            const A = P0(lo), B = P0(hi), w = Math.hypot(B[0] - A[0], B[1] - A[1]);
            // reveal jambs (wall end faces) at both ends — common to all opening kinds
            const tick = (Pt) => g.append(svgEl('line', { x1: Pt[0] - nx * h, y1: Pt[1] - ny * h, x2: Pt[0] + nx * h, y2: Pt[1] + ny * h, stroke: col, 'stroke-width': 2.4, class: 'gp-openreveal' }));
            tick(A); tick(B);
            if (o.kind === 'window') {
                // two parallel glass panes + a thin sill/breast line → unmistakably a window
                g.append(svgEl('line', { x1: A[0] - nx * h * 0.4, y1: A[1] - ny * h * 0.4, x2: B[0] - nx * h * 0.4, y2: B[1] - ny * h * 0.4, stroke: col, 'stroke-width': 1.6, class: 'gp-openglass' }));
                g.append(svgEl('line', { x1: A[0] + nx * h * 0.4, y1: A[1] + ny * h * 0.4, x2: B[0] + nx * h * 0.4, y2: B[1] + ny * h * 0.4, stroke: col, 'stroke-width': 1.6, class: 'gp-openglass' }));
                g.append(svgEl('line', { x1: A[0], y1: A[1], x2: B[0], y2: B[1], stroke: col, 'stroke-width': 1, opacity: 0.5 }));
            } else if (o.kind === 'door') {
                // leaf hinged at one end, swinging to one side of the wall (interactive: side/hinge)
                const side = o.side || 1, hingeAtA = !o.hinge, hinge = hingeAtA ? A : B, jamb = hingeAtA ? B : A;
                const leaf = [hinge[0] + nx * side * w, hinge[1] + ny * side * w];
                // the swing area colours red where it would open into a vehicle/Stellfläche (S3-A)
                const dcol = sweepHitsSpot(doorSweepQuad(e, o)) ? '#ff6b6b' : col;
                g.append(svgEl('line', { x1: hinge[0], y1: hinge[1], x2: leaf[0], y2: leaf[1], stroke: dcol, 'stroke-width': 2.4 }));
                const a0 = Math.atan2(leaf[1] - hinge[1], leaf[0] - hinge[0]); let a1 = Math.atan2(jamb[1] - hinge[1], jamb[0] - hinge[0]), da = a1 - a0;
                while (da > Math.PI) da -= 2 * Math.PI; while (da < -Math.PI) da += 2 * Math.PI;
                let d = 'M ' + leaf[0] + ' ' + leaf[1]; for (let s = 1; s <= 10; s++) { const a = a0 + da * s / 10; d += ' L ' + (hinge[0] + Math.cos(a) * w) + ' ' + (hinge[1] + Math.sin(a) * w); }
                g.append(svgEl('path', { d, fill: 'none', stroke: dcol, 'stroke-width': 1.2, 'stroke-dasharray': '2 3', opacity: 0.85 }));
            } else {
                // gate (Sektional-/Rolltor): guide rails along both edges + lamella ticks across
                g.append(svgEl('line', { x1: A[0] - nx * h, y1: A[1] - ny * h, x2: B[0] - nx * h, y2: B[1] - ny * h, stroke: col, 'stroke-width': 1.6, opacity: 0.9 }));
                g.append(svgEl('line', { x1: A[0] + nx * h, y1: A[1] + ny * h, x2: B[0] + nx * h, y2: B[1] + ny * h, stroke: col, 'stroke-width': 1.6, opacity: 0.9 }));
                const dxn = (B[0] - A[0]) / (w || 1), dyn = (B[1] - A[1]) / (w || 1), n = Math.max(2, Math.round(w / 7));
                for (let s = 0; s <= n; s++) { const px = A[0] + dxn * (w * s / n), py = A[1] + dyn * (w * s / n); g.append(svgEl('line', { x1: px - nx * h, y1: py - ny * h, x2: px + nx * h, y2: py + ny * h, stroke: col, 'stroke-width': 1.4, opacity: 0.7 })); }
            }
            if (P.structSel && P.structSel.type === 'opening' && P.structSel.ei === edgeIndex(e) && P.structSel.oi === oi) {
                g.append(svgEl('rect', { x: Math.min(A[0], B[0]) - h - 2, y: Math.min(A[1], B[1]) - h - 2, width: Math.abs(B[0] - A[0]) + th + 4, height: Math.abs(B[1] - A[1]) + th + 4, fill: 'none', stroke: '#fff', 'stroke-width': 1, 'stroke-dasharray': '3 2', opacity: 0.85 }));
                [A, B].forEach((Pt) => g.append(svgEl('rect', { x: Pt[0] - 4, y: Pt[1] - 4, width: 8, height: 8, class: 'gp-ohandle' })));
                // Rohbau + lichtes Durchgangsmaß (Türen/Tore) as a small caption above the opening (S3-B).
                if (o.kind === 'door' || o.kind === 'gate') { const mid = [(A[0] + B[0]) / 2, (A[1] + B[1]) / 2];
                    const lab = svgEl('text', { x: mid[0] + nx * (h + 12), y: mid[1] + ny * (h + 12) + 3, 'text-anchor': 'middle', class: 'gp-walllab sub' });
                    lab.textContent = 'lichte ' + clearWidth(o).toFixed(2).replace('.', ',') + ' m'; g.append(lab); }
            }
        }
        function edgeIndex(e) { return P.walls.edges.indexOf(e); }
        // Vehicle buffer visualisation: a HALF-buffer band only on the side(s) where another
        // vehicle actually sits within the clearance — never toward walls/lanes/maint/empty.
        function drawBuffers() {
            if (!P.buffer) return;
            const CELL = P.CELL, half = P.bufferM / 2, g = svgEl('g', { class: 'gp-bufg' });
            const vs = P.spots.filter((s) => !s.noBuf);
            for (let i = 0; i < vs.length; i++) {
                const a = vs[i], ah = halfAABB(a), acx = a.x + a.w / 2, acy = a.y + a.h / 2, ax0 = acx - ah.hx, ax1 = acx + ah.hx, ay0 = acy - ah.hy, ay1 = acy + ah.hy;
                let L = false, R = false, T = false, B = false;
                for (let j = 0; j < vs.length; j++) { if (i === j) continue; const b = vs[j], bh = halfAABB(b), bcx = b.x + b.w / 2, bcy = b.y + b.h / 2, bx0 = bcx - bh.hx, bx1 = bcx + bh.hx, by0 = bcy - bh.hy, by1 = bcy + bh.hy;
                    if (ay0 < by1 - 0.02 && ay1 > by0 + 0.02) { if (bx0 - ax1 >= -0.02 && bx0 - ax1 < P.bufferM) R = true; if (ax0 - bx1 >= -0.02 && ax0 - bx1 < P.bufferM) L = true; }
                    if (ax0 < bx1 - 0.02 && ax1 > bx0 + 0.02) { if (by0 - ay1 >= -0.02 && by0 - ay1 < P.bufferM) B = true; if (ay0 - by1 >= -0.02 && ay0 - by1 < P.bufferM) T = true; }
                }
                const band = (x, y, w, h) => { if (w > 0 && h > 0) g.append(svgEl('rect', { x: x * CELL, y: y * CELL, width: w * CELL, height: h * CELL, class: 'gp-bufband' })); };
                if (R) band(ax1, ay0, half, ay1 - ay0); if (L) band(ax0 - half, ay0, half, ay1 - ay0);
                if (B) band(ax0, ay1, ax1 - ax0, half); if (T) band(ax0, ay0 - half, ax1 - ax0, half);
            }
            svg.append(g);
        }
        // ---- wall rendering: clean butt-capped segments, rounded node joints, cut-out openings,
        //      total node-to-node length (offset) + optional clear sub-segment lengths ----
        function drawWalls() {
            const CELL = P.CELL, g = svgEl('g', { class: 'gp-wallg' });
            const wcs = wallCorners();
            P.walls.edges.forEach((e, ei) => {
                const wc = wcs[ei]; if (!wc) return;
                const col = WALLCOL[e.kind] || '#8aa0b6', th = Math.max(2, (e.thick || 0.24) * CELL);
                const selE = P.structSel && P.structSel.type === 'edge' && P.structSel.idx === ei;
                // offset (mitred) centre line = midpoint of the two boundaries, for openings + labels
                const oa = { x: (wc.aL.x + wc.aR.x) / 2, y: (wc.aL.y + wc.aR.y) / 2 }, ob = { x: (wc.bL.x + wc.bR.x) / 2, y: (wc.bL.y + wc.bR.y) / 2 };
                const vd = { a: oa, b: ob, dx: ob.x - oa.x, dy: ob.y - oa.y, len: Math.hypot(ob.x - oa.x, ob.y - oa.y) }; if (vd.len < 0.02) return;
                const P0 = (u) => [(oa.x + vd.dx * u) * CELL, (oa.y + vd.dy * u) * CELL];
                // Boundary point at param u: the true mitre corner at the wall ENDS (u=0/1), but a
                // PERPENDICULAR cut everywhere in between — so an opening punches a clean, square
                // hole (the mitre-length ≠ centre-length mismatch no longer bleeds wall into the gap).
                const perpx = -vd.dy / vd.len, perpy = vd.dx / vd.len, hw = (e.thick || 0.24) / 2;
                const leftAt = (u) => u <= 1e-6 ? [wc.aL.x * CELL, wc.aL.y * CELL] : u >= 1 - 1e-6 ? [wc.bL.x * CELL, wc.bL.y * CELL] : [(oa.x + vd.dx * u + perpx * hw) * CELL, (oa.y + vd.dy * u + perpy * hw) * CELL];
                const rightAt = (u) => u <= 1e-6 ? [wc.aR.x * CELL, wc.aR.y * CELL] : u >= 1 - 1e-6 ? [wc.bR.x * CELL, wc.bR.y * CELL] : [(oa.x + vd.dx * u - perpx * hw) * CELL, (oa.y + vd.dy * u - perpy * hw) * CELL];
                // o.c is stored on the DRAWN edge; map it onto the offset centre line so the gap
                // stays under the symbol even at large corner mitres (S3-D).
                const v0 = edgeVec(e), mapC = (oc) => { const wx = v0.a.x + v0.dx * oc, wy = v0.a.y + v0.dy * oc; return Math.max(0, Math.min(1, ((wx - oa.x) * vd.dx + (wy - oa.y) * vd.dy) / (vd.len * vd.len))); };
                const spans = (e.ops || []).map((o, oi) => { const cc = mapC(o.c), hp = (o.w / vd.len) / 2; return { lo: Math.max(0, cc - hp), hi: Math.min(1, cc + hp), oi, cc }; }).sort((a, b) => a.lo - b.lo);
                // filled, mitre-joined wall body — drawn in pieces so openings leave clean gaps
                const piece = (u0, u1) => { if (u1 - u0 < 1e-4) return; const l0 = leftAt(u0), l1 = leftAt(u1), r1 = rightAt(u1), r0 = rightAt(u0); g.append(svgEl('polygon', { points: l0[0] + ',' + l0[1] + ' ' + l1[0] + ',' + l1[1] + ' ' + r1[0] + ',' + r1[1] + ' ' + r0[0] + ',' + r0[1], fill: col, class: 'gp-wallpoly' + (selE ? ' sel' : '') })); };
                let t0 = 0; for (const sp of spans) { piece(t0, sp.lo); t0 = sp.hi; } piece(t0, 1);
                spans.forEach((sp) => drawOpening(g, vd, e, sp.oi, CELL, sp.cc));
                if (P.mode === 'plan' && vd.len > 0.3) {
                    let nx = -vd.dy, ny = vd.dx; const nl = Math.hypot(nx, ny) || 1; nx /= nl; ny /= nl;
                    const mid = P0(0.5), off = th / 2 + 11;
                    // With openings, place the total on the SAME outer side as the sub-segment labels,
                    // not the inner side: zone/vehicle DOM blocks paint ABOVE this SVG text, so a total
                    // sitting at the mid-wall opening was hidden behind the lane (verified against real
                    // data: lane y6.4–10.4, x→38.1 covered the inner-side total at the y8.4 gate). Its
                    // mid-wall position sits between the two segment labels, so they don't clash. No
                    // openings → keep the inner side as before (no regression on plain walls).
                    const tsg = spans.length ? -1 : 1;
                    const tl = svgEl('text', { x: mid[0] + tsg * nx * off, y: mid[1] + tsg * ny * off + 3, 'text-anchor': 'middle', class: 'gp-walllab' }); tl.textContent = labelLen(ei).toFixed(2).replace('.', ',') + ' m'; g.append(tl); // dimension per Bezug (Innen/Achse/Außen)
                    if (spans.length) { // clear sub-segment lengths — measured on the DRAWN edge (v0)
                        // and trimmed to the SAME Bezug reference as the total (labelLen), so the pieces
                        // plus the opening widths add up to the displayed length instead of overshooting
                        // by the mitre/offset the rendered body gains at the corners. (Innen: full v.len;
                        // Achse/Außen deduct the neighbour-wall thickness at each corner end.)
                        const inset = (ni) => P.wallRef === 'inner' ? 0 : nodePerpHalf(ni, ei) * (P.wallRef === 'outer' ? 2 : 1);
                        const dsp = (e.ops || []).map((o) => { const hp2 = (o.w / v0.len) / 2; return [Math.max(0, o.c - hp2), Math.min(1, o.c + hp2)]; }).sort((a, b) => a[0] - b[0]);
                        const segs = []; let s0 = 0; for (const [lo, hi] of dsp) { if (lo - s0 > 0.002) segs.push([s0, lo]); s0 = hi; } if (1 - s0 > 0.002) segs.push([s0, 1]);
                        for (const [u0, u1] of segs) { let wm = (u1 - u0) * v0.len; if (u0 < 0.002) wm -= inset(e.a); if (u1 > 0.998) wm -= inset(e.b); if (wm < 0.01 || wm * CELL < 26) continue; const m2 = P0((u0 + u1) / 2); const st = svgEl('text', { x: m2[0] - nx * (th / 2 + 9), y: m2[1] - ny * (th / 2 + 9) + 3, 'text-anchor': 'middle', class: 'gp-walllab sub' }); st.textContent = wm.toFixed(2).replace('.', ',') + ' m'; g.append(st); }
                    }
                }
            });
            if (P.mode === 'plan' && !P.calib && !P.tool) { // node handles only in structure-edit mode
                P.walls.nodes.forEach((n, i) => { const jp = nodeJoint(i); const sel = P.structSel && P.structSel.type === 'node' && P.structSel.idx === i; g.append(svgEl('circle', { cx: jp.x * CELL, cy: jp.y * CELL, r: sel ? 7 : 5, class: 'gp-wnode' + (sel ? ' sel' : ''), 'data-node': i })); });
            }
            svg.append(g);
        }
        function drawWallPreview() {
            if (P.mode !== 'plan') return;
            const CELL = P.CELL;
            // orthogonal snap guide-line (spans the canvas)
            if (P.guide && (P.chain || nodeDrag)) {
                if (P.guide.kind === 'h') svg.append(svgEl('line', { x1: 0, y1: P.guide.y * CELL, x2: P.Wm * CELL, y2: P.guide.y * CELL, class: 'gp-guide' }));
                else svg.append(svgEl('line', { x1: P.guide.x * CELL, y1: 0, x2: P.guide.x * CELL, y2: P.Hm * CELL, class: 'gp-guide' }));
            }
            // dynamic distance readout: attach/hover point → both ends of the existing wall
            if (P.attach && (P.chain || isOpenKind(P.tool))) { const ed = P.walls.edges[P.attach.ei]; if (ed) { const v = edgeVec(ed);
                const dA = P.attach.t * v.len, dB = (1 - P.attach.t) * v.len;
                const lab = (nx, ny, txt) => { const tt = svgEl('text', { x: (nx + P.attach.x) / 2 * CELL, y: (ny + P.attach.y) / 2 * CELL - 4, 'text-anchor': 'middle', class: 'gp-distlab' }); tt.textContent = txt; svg.append(tt); };
                if (dA > 0.05) lab(v.a.x, v.a.y, dA.toFixed(2).replace('.', ',') + ' m');
                if (dB > 0.05) lab(v.b.x, v.b.y, dB.toFixed(2).replace('.', ',') + ' m');
            } }
            // T-junction anchor: keep showing the attach point's distances to the tapped wall's
            // ends throughout the whole chain, so 90° branches can be placed precisely.
            if (P.chainAnchor && P.chain) { const an = P.chainAnchor;
                const dA = Math.hypot(an.x - an.aEnd.x, an.y - an.aEnd.y), dB = Math.hypot(an.x - an.bEnd.x, an.y - an.bEnd.y);
                const lab2 = (ex, ey, d) => { if (d < 0.05) return; const tt = svgEl('text', { x: (an.x + ex) / 2 * CELL, y: (an.y + ey) / 2 * CELL - 4, 'text-anchor': 'middle', class: 'gp-distlab' }); tt.textContent = d.toFixed(2).replace('.', ',') + ' m'; svg.append(tt); };
                lab2(an.aEnd.x, an.aEnd.y, dA); lab2(an.bEnd.x, an.bEnd.y, dB);
                svg.append(svgEl('circle', { cx: an.x * CELL, cy: an.y * CELL, r: 4, class: 'gp-anchor' }));
            }
            if (P.chain && P.chain.length && P.preview && isWallDraw(P.tool)) {
                const last = P.walls.nodes[P.chain[P.chain.length - 1]];
                if (last) {
                    const ax = last.x * CELL, ay = last.y * CELL, bx = P.preview.x * CELL, by = P.preview.y * CELL;
                    const th = Math.max(2, ((EXCL[P.tool] && EXCL[P.tool].h) || 0.24) * CELL);
                    svg.append(svgEl('line', { x1: ax, y1: ay, x2: bx, y2: by, stroke: WALLCOL[P.tool] || '#8aa0b6', 'stroke-width': th, 'stroke-linecap': 'round', 'stroke-dasharray': '2 6', opacity: 0.85, class: 'gp-wallpreview' }));
                    const len = Math.hypot(P.preview.x - last.x, P.preview.y - last.y);
                    const drawTh = P.wallThick || (EXCL[P.tool] && EXCL[P.tool].h) || 0.24, ph = nodePerpHalf(P.chain[P.chain.length - 1], -1);
                    const clr = P.wallRef === 'inner' ? len : P.wallRef === 'outer' ? Math.max(0, len - 2 * ph - drawTh) : Math.max(0, len - ph - drawTh / 2); // live dim per Bezug
                    const tl = svgEl('text', { x: (ax + bx) / 2, y: (ay + by) / 2 - 6, 'text-anchor': 'middle', class: 'gp-walllab live' }); tl.textContent = clr.toFixed(2).replace('.', ',') + ' m'; svg.append(tl);
                }
                for (const idx of P.chain) { const n = P.walls.nodes[idx]; if (n) svg.append(svgEl('circle', { cx: n.x * CELL, cy: n.y * CELL, r: 4, class: 'gp-wnode chain' })); }
            }
            if (P.openStart && P.preview && isOpenKind(P.tool)) {
                const e = P.walls.edges[P.openStart.ei];
                if (e) { const v = edgeVec(e), t2 = P.preview.t != null ? P.preview.t : P.openStart.t;
                    const x1 = (v.a.x + v.dx * P.openStart.t) * CELL, y1 = (v.a.y + v.dy * P.openStart.t) * CELL, x2 = (v.a.x + v.dx * t2) * CELL, y2 = (v.a.y + v.dy * t2) * CELL;
                    svg.append(svgEl('line', { x1, y1, x2, y2, stroke: OPENCOL[P.tool] || '#4ea8f5', 'stroke-width': Math.max(3, (e.thick || 0.24) * CELL * 0.7), 'stroke-linecap': 'butt', opacity: 0.9, class: 'gp-openpreview' }));
                    const w = Math.abs(t2 - P.openStart.t) * v.len; const tl = svgEl('text', { x: (x1 + x2) / 2, y: (y1 + y2) / 2 - 6, 'text-anchor': 'middle', class: 'gp-walllab live' }); tl.textContent = w.toFixed(2).replace('.', ',') + ' m'; svg.append(tl); }
            }
            if (zoneDraw) {
                const x = Math.min(zoneDraw.x0, zoneDraw.x1), y = Math.min(zoneDraw.y0, zoneDraw.y1), w = Math.abs(zoneDraw.x1 - zoneDraw.x0), h = Math.abs(zoneDraw.y1 - zoneDraw.y0), col = ZONECOL[P.tool] || '#e0b452';
                svg.append(svgEl('rect', { x: x * CELL, y: y * CELL, width: w * CELL, height: h * CELL, fill: col, 'fill-opacity': 0.14, stroke: col, 'stroke-width': 1.5, 'stroke-dasharray': '5 3' }));
                const tl = svgEl('text', { x: (x + w / 2) * CELL, y: (y + h / 2) * CELL + 4, 'text-anchor': 'middle', class: 'gp-walllab live' }); tl.textContent = w.toFixed(1).replace('.', ',') + ' × ' + h.toFixed(1).replace('.', ',') + ' m'; svg.append(tl);
            }
            if (P.snapHint) svg.append(svgEl('circle', { cx: P.snapHint.x * CELL, cy: P.snapHint.y * CELL, r: 6, class: 'gp-snaphint' }));
        }
        function renderMetrics() {
            metrics.innerHTML = '';
            const total = polyArea(P.floor); let ex = 0, ve = 0; P.excl.forEach((b) => { if (!isZoneKind(b.kind)) ex += b.w * b.h; }); P.spots.forEach((b) => ve += b.w * b.h);
            // Hall dimensions = the ACTUAL floor polygon's bounding box (same source as the
            // Breite/Tiefe readout), not the P.Wm×P.Hm canvas grid — after „Form bearbeiten"
            // shrinks the polygon inside the grid the two diverge, and the bbox is the real one.
            const bb = floorBB(), fw = bb.maxX - bb.minX, fh = bb.maxY - bb.minY;
            const m = (val, label, cls) => el('div', { class: 'gp-metric' }, el('div', { class: 'gp-mv ' + (cls || '') }, val), el('div', { class: 'gp-ml' }, label));
            metrics.append(
                m(Math.round(Math.max(0, total - ex - ve)) + ' m²', 'Frei', 'ok'),
                m(Math.round(ve) + ' m²', 'Belegt · ' + P.spots.length, 'busy'),
                m(Math.round(ex) + ' m²', 'Ausgenommen', 'excl'),
                m(fw.toFixed(1).replace('.', ',') + '×' + fh.toFixed(1).replace('.', ',') + ' m', 'Halle · ' + Math.round(total) + ' m²'),
                m(polyPerim(P.floor).toFixed(1) + ' m', 'Umfang'));
            const enc = P.autoArea ? enclosure() : null;
            if (enc && enc.area > 0.3) { const nr = (enc.rooms || []).length; metrics.append(m(enc.area.toFixed(1).replace('.', ',') + ' m²', 'Parkfläche' + (nr > 1 ? ' · ' + nr + ' Räume' : ' · Wände'), 'ok')); } // 1 decimal, matching the plan label (S1-D)
        }

        // ---- toolbar ----
        function renderToolbar() {
            toolbar.innerHTML = '';
            const tb = (label, title, fn, on, cls) => { const b = el('button', { class: 'gp-tbtn ' + (cls || '') + (on ? ' on' : ''), title: title || label, onclick: fn }, label); return b; };
            const undoB = tb('↶', 'Rückgängig (Strg+Z)', doUndo); undoB.disabled = P.hpos <= 0;
            const redoB = tb('↷', 'Wiederholen (Strg+Umschalt+Z)', doRedo); redoB.disabled = P.hpos >= P.hist.length - 1;
            toolbar.append(undoB, redoB);
            if (canManageNow && P.mode === 'plan') toolbar.append(tb('⊾ 90°', 'Rechtwinklig einrasten', () => { P.ortho = !P.ortho; renderToolbar(); toast(P.ortho ? 'Rechtwinklig an' : 'Rechtwinklig aus'); }, P.ortho));
            // Snap selector: free, or snap every ¼ / ½ / 1 m (applies to floor edits
            // and block moves alike).
            const seg = el('div', { class: 'gp-seg', title: 'Raster: freie Abstände oder feste Schrittweite' });
            seg.append(el('button', { class: !P.snap ? 'on' : '', onclick: () => { P.snap = false; renderToolbar(); } }, 'Frei'));
            [['0,1 m', 0.1], ['¼ m', 0.25], ['½ m', 0.5], ['1 m', 1]].forEach(([lab, step]) => seg.append(el('button', { class: (P.snap && P.gridStep === step) ? 'on' : '', onclick: () => { P.snap = true; P.gridStep = step; renderToolbar(); } }, lab)));
            toolbar.append(seg);
            // Auto-Snap: objects fang flush against each other (in addition to the floor line).
            if (canManageNow) toolbar.append(tb('🧲', 'Auto-Snap: Objekte fangen aneinander', () => { P.autoSnap = !P.autoSnap; renderToolbar(); toast(P.autoSnap ? 'Auto-Snap an' : 'Auto-Snap aus'); }, P.autoSnap));
            if (canManageNow) toolbar.append(tb('🛡', 'Pufferzonen zwischen Fahrzeugen (Taste P)', () => { P.buffer = !P.buffer; renderToolbar(); draw(); toast(P.buffer ? 'Pufferzonen an · ' + P.bufferM.toFixed(1).replace('.', ',') + ' m' : 'Pufferzonen aus'); }, P.buffer));
            // Vehicle display mode (client-side preference, remembered): symbol / photo / rectangle.
            if (P.mode === 'manage') { const dseg = el('div', { class: 'gp-seg', title: 'Fahrzeug-Darstellung' });
                [['◈', 'symbol', 'Symbol'], ['▣', 'foto', 'Foto'], ['▭', 'rect', 'Rechteck']].forEach(([ic, key, t]) => dseg.append(el('button', { class: P.render === key ? 'on' : '', title: t, onclick: () => { P.render = key; try { localStorage.setItem('gp.render', key); } catch (e) { /* private mode */ } renderToolbar(); draw(); } }, ic)));
                toolbar.append(dseg); }
            // Manage custom planner icons (upload / rename / replace / delete) — placed on
            // the toolbar so it's reachable without first selecting a vehicle.
            if (canManageNow && P.mode === 'manage') toolbar.append(tb('🖼 Icons', 'Eigene Planer-Icons verwalten', async () => { await plannerIconsDialog(); try { P.plannerIcons = await api.get('/planner-icons'); } catch (e) { /* keep old */ } draw(); if (P.sel) renderRail(); }));
            if (canManageNow && P.mode === 'manage') { const rb = tb('⟳ Drehen', 'Auswahl drehen', rotateSel); rb.disabled = !(P.sel && P.spots.find((s) => s._id === P.sel)); toolbar.append(rb); }
            toolbar.append(tb('−', 'Verkleinern', () => { P.zoom = Math.max(0.5, +(P.zoom - 0.15).toFixed(2)); layout(); }));
            toolbar.append(el('span', { class: 'gp-zoomlbl' }, Math.round((P.zoom || 1) * 100) + '%'));
            toolbar.append(tb('+', 'Vergrößern', () => { P.zoom = Math.min(8, +(P.zoom + 0.15).toFixed(2)); layout(); }));
            toolbar.append(tb('⤢ Passen', 'Einpassen & Raster an die Wände anpassen', () => { fitView(); }));
            toolbar.append(tb('⭳ Export', 'Als PNG / PDF / SVG / DXF exportieren', () => openExportMenu()));
            if (canManageNow) toolbar.append(tb('▤ Vorlagen', 'Wand-Layouts speichern & wiederverwenden', () => openTemplateMenu()));
            toolbar.append(tb('?', 'Tastaturkürzel (Taste ?)', () => toggleShortcutHelp()));
            // Always render the save control (fixed width, centred) so it never appears/disappears
            // and reflows the wrapping toolbar mid-edit — that shift caused mis-clicks. Dirty → an
            // active "● Speichern"; clean → a disabled "✓ Gespeichert" status in the same slot.
            if (canManageNow) { const sv = tb(P.dirty ? '● Speichern' : '✓ Gespeichert', P.dirty ? 'Jetzt speichern (Auto-Save aktiv)' : 'Alle Änderungen gespeichert', () => { if (P.dirty) doSaveGeom(); }, false, P.dirty ? 'btn-primary' : ''); sv.disabled = !P.dirty; sv.style.minWidth = '7.5rem'; sv.style.textAlign = 'center'; toolbar.append(sv); }
        }
        function rotateSel() {
            const b = P.spots.find((s) => s._id === P.sel); if (!b) return;
            const old = b.rot || 0; b.rot = (old + 90) % 360;
            if (!validVeh(b, b._id)) { b.rot = old; toast('Drehen nicht möglich', 'error'); return; }
            b._dirty = true; markDirty(); pushUndo(); draw(); toast('Gedreht');
        }

        // ---- persistence ----
        const round2 = (v) => Math.round(v * 100) / 100;
        function persistSpot(b) {
            // Payload is snapshotted NOW; writes for the same spot are chained on
            // b._wq so rapid undo/redo can't land PUTs out of order (last call wins).
            // Persist the SPOT's own unique label (hall-unique, uq_spots_hall_label), NOT the
            // vehicle display label — two like-named vehicles must not collide on save.
            const label = b.spotLabel || b.label, geometry = { x: round2(b.x), y: round2(b.y), w: round2(b.w), h: round2(b.h), rot: Math.round(b.rot || 0), status: b.status, noBuf: b.noBuf || undefined };
            b._wq = (b._wq || Promise.resolve())
                .then(() => api.put('/spots/' + b._id, { label, geometry }))
                .then(() => { b._dirty = false; }, (err) => { b._dirty = true; P.dirty = true; renderToolbar(); toast(err.message || 'Speichern fehlgeschlagen', 'error'); });
            return b._wq;
        }
        // Persist a resized vehicle's footprint as its real Länge×Breite via the existing
        // dimensions endpoint (chained on b._dq so rapid resizes can't land out of order).
        function persistDims(b) {
            if (!b.vehId) return Promise.resolve();
            const body = { length_m: round2(b.w), width_m: round2(b.h), height_m: b.H == null ? null : b.H, weight_t: b.t == null ? null : b.t };
            const pl = P.palette.find((x) => x.id === b.vehId); if (pl) { pl.length_m = body.length_m; pl.width_m = body.width_m; }
            b._dq = (b._dq || Promise.resolve())
                .then(() => api.put('/vehicles/' + b.vehId + '/dimensions', body))
                .then(() => {}, (err) => { toast(err.message || 'Maße speichern fehlgeschlagen', 'error'); });
            return b._dq;
        }
        function currentWalls() {
            return { nodes: P.walls.nodes.map((n) => ({ x: round2(n.x), y: round2(n.y) })), edges: P.walls.edges.map((e) => ({ a: e.a, b: e.b, kind: e.kind, thick: round2(e.thick), ops: (e.ops && e.ops.length) ? e.ops.map((o) => ({ c: round2(o.c), w: round2(o.w), kind: o.kind, side: o.side, hinge: o.hinge, frame: o.frame })) : undefined })) };
        }
        function currentGeometry() { return { floor: P.floor, Wm: P.Wm, Hm: P.Hm, tor: P.tor, load: P.load, shape: P.shape, excl: P.excl.map((e) => ({ kind: e.kind, x: round2(e.x), y: round2(e.y), w: round2(e.w), h: round2(e.h), rot: Math.round(e.rot || 0), label: e.label, mat: e.mat || undefined })), walls: (P.walls.edges.length ? currentWalls() : undefined), wallRef: P.wallRef !== 'axis' ? P.wallRef : undefined, plan: P.plan ? { href: P.plan.href, x: round2(P.plan.x), y: round2(P.plan.y), w: round2(P.plan.w), h: round2(P.plan.h), opacity: round2(P.plan.opacity), hidden: P.plan.hidden || undefined } : undefined }; }
        async function doSaveGeom(silent) {
            P.dirty = false; renderToolbar(); // optimistic; a change during the save re-flags it
            try {
                await api.put('/halls/' + P.hallId, { name: P.hallName, geometry: currentGeometry() });
                // Free placement: an invalid spot (outside / gate / lane / collision) is kept
                // locally and stays _dirty, but is NOT persisted until it is valid again.
                let heldInvalid = 0;
                for (const b of P.spots) {
                    if (!b._dirty) continue;
                    if (!validVeh(b, b._id)) { b._invalid = true; heldInvalid++; continue; }
                    b._invalid = false;
                    await persistSpot(b);
                }
                // A failed spot write re-flags P.dirty (persistSpot); reschedule instead of
                // falsely reporting success.
                if (P.dirty) scheduleSave();
                else if (heldInvalid && !silent) toast(heldInvalid + (heldInvalid === 1 ? ' Fahrzeug ungültig platziert — nicht gespeichert' : ' Fahrzeuge ungültig platziert — nicht gespeichert'), 'warn');
                else if (!silent) toast('Grundriss gespeichert', 'ok');
            } catch (err) {
                // A 400 means the geometry is rejected as-is (almost always: too large — a
                // heavy Bauplan underlay). Retrying can't help, so surface it even on the
                // silent auto-save path and DON'T reschedule an endless failing loop.
                if (err.status === 400) { toast('Grundriss zu groß zum Speichern — Bauplan verkleinern/entfernen', 'error'); P.dirty = true; renderToolbar(); return; }
                if (!silent) toast(err.message || 'Speichern fehlgeschlagen', 'error');
                P.dirty = true; renderToolbar(); scheduleSave();
            }
        }
        async function dupHall() {
            try { const hl = await api.post('/garages/' + P.garageId + '/halls', { name: P.hallName + ' (Kopie)', geometry: currentGeometry() }); toast('Halle dupliziert', 'ok'); navigate('hall/' + hl.id); }
            catch (err) { toast(err.message || 'Duplizieren fehlgeschlagen', 'error'); }
        }
        async function delHallHere() {
            if (!await confirmDialog('Halle löschen?', `„${P.hallName}" samt Stellplätzen wird entfernt. Zugewiesene Gefährte werden freigegeben.`, 'Löschen')) return;
            try { await api.del('/halls/' + P.hallId); toast('Halle gelöscht'); navigate('garage/' + P.garageId); }
            catch (err) { toast(err.message || 'Löschen fehlgeschlagen', 'error'); }
        }
        // Edit a Gefährt's physical measurements (fixes the "Maße offen" dead-end).
        async function dimForm(veh) {
            const vf = [['length_m', 'Länge (m)', 'L'], ['width_m', 'Breite (m)', 'W'], ['height_m', 'Höhe (m)', 'H'], ['weight_t', 'Gewicht (t)', 't']];
            await formModal({ title: 'Maße · ' + (veh.label || 'Gefährt'), submitLabel: 'Speichern',
                fields: vf.map(([n, lab, key]) => ({ name: n, label: lab, type: 'number', step: '0.1', value: veh[key] != null ? veh[key] : '' })),
                save: async (f) => {
                    const body = {}; vf.forEach(([n]) => { const v = f[n]; body[n] = (v === '' || v == null) ? null : Number(v); });
                    await api.put('/vehicles/' + veh.id + '/dimensions', body);
                    const sp = P.spots.find((s) => s.vehId === veh.id);
                    if (sp) {
                        sp.L = body.length_m; sp.W = body.width_m; sp.H = body.height_m; sp.t = body.weight_t;
                        // Scale the placed footprint to the real Länge×Breite (maßstabsgetreu).
                        // Safe now: the .gp-rot class collision that caused the oval/no-click is
                        // gone, and blocks are crisp rectangles (border-radius:3).
                        if (body.length_m != null && body.width_m != null) { sp.w = body.length_m; sp.h = body.width_m; sp._dirty = true; markDirty(); }
                    }
                    const pl = P.palette.find((x) => x.id === veh.id); if (pl) { pl.length_m = body.length_m; pl.width_m = body.width_m; pl.height_m = body.height_m; pl.weight_t = body.weight_t; }
                } });
            draw();
        }

        // ---- rail ----
        function renderRail() { rail.innerHTML = ''; if (P.mode === 'manage') renderManageRail(); else renderPlanRail(); }
        // Hall switcher (available in BOTH modes); withCrud adds the management buttons.
        function hallSwitchCard(withCrud) {
            const card = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Halle', el('span', { class: 'eyebrow' }, P.garageName)));
            const sel = el('select', { class: 'gp-select', 'aria-label': 'Halle wählen', onchange: async (e) => {
                const v = Number(e.target.value); if (!v || v === P.hallId) return;
                if (P.dirty && !(await confirmDialog('Halle wechseln?', 'Ungespeicherte Grundriss-Änderungen gehen verloren.', 'Wechseln'))) { e.target.value = String(P.hallId); return; }
                navigate('hall/' + v);
            } });
            (P.halls || []).forEach((hl) => { const o = el('option', { value: hl.id }, hl.name); if (hl.id === P.hallId) o.selected = true; sel.append(o); });
            card.append(el('div', { class: 'gp-hallrow' }, sel));
            if (withCrud && canManageNow) card.append(el('div', { class: 'gp-addrow', style: 'margin-top:.5rem' },
                el('button', { class: 'btn btn-sm', onclick: () => hallForm(P.garageId) }, '+ Neue Halle'),
                el('button', { class: 'btn btn-sm', onclick: () => hallForm(P.garageId, { id: P.hallId, name: P.hallName, geometry: currentGeometry() }) }, 'Umbenennen'),
                el('button', { class: 'btn btn-sm', onclick: dupHall }, 'Duplizieren'),
                el('button', { class: 'btn btn-sm btn-danger', onclick: delHallHere }, 'Löschen')));
            return card;
        }
        function vehCard(p) {
            const c = el('div', { class: 'gp-vehc', title: 'Auf die Fläche ziehen' }); c.dataset.pid = p.id;
            const hasDims = (p.length_m != null && p.width_m != null && p.height_m != null);
            let dimNode;
            if (hasDims) dimNode = el('div', { class: 'gp-vdm num' }, p.length_m.toFixed(1) + ' × ' + p.width_m.toFixed(1) + ' × ' + p.height_m.toFixed(1) + ' m' + (p.weight_t != null ? ' · ' + p.weight_t.toFixed(2).replace('.', ',') + ' t' : ''));
            else if (canManageNow) { dimNode = el('button', { class: 'gp-dimopen' }, 'Maße festlegen →'); dimNode.addEventListener('pointerdown', (e) => e.stopPropagation()); dimNode.addEventListener('click', (e) => { e.stopPropagation(); dimForm({ id: p.id, label: p.label, L: p.length_m, W: p.width_m, H: p.height_m, t: p.weight_t }); }); }
            else dimNode = el('div', { class: 'gp-vdm num' }, 'Maße offen');
            c.append(el('span', { class: 'gp-vic' }, '🚗'),
                el('div', { style: 'min-width:0' }, el('div', { class: 'gp-vnm' }, p.label, p.type ? el('span', { class: 'gp-chip' }, p.type) : null), dimNode));
            if (canManageNow) {
                const ed = el('button', { class: 'gp-vedit', title: 'Maße bearbeiten' }, '✎');
                ed.addEventListener('pointerdown', (e) => e.stopPropagation());
                ed.addEventListener('click', (e) => { e.stopPropagation(); dimForm({ id: p.id, label: p.label, L: p.length_m, W: p.width_m, H: p.height_m, t: p.weight_t }); });
                c.append(ed);
            } else { c.append(el('span', { class: 'gp-vst' }, '⠿')); }
            return c;
        }
        function renderManageRail() {
            rail.append(hallSwitchCard(false));
            const palCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Nicht platziert', el('span', { class: 'eyebrow' }, P.palette.length + ' · ziehen →')));
            const search = el('input', { class: 'gp-palsearch', type: 'search', placeholder: 'Gefährt suchen…', value: P.palQuery || '' });
            const pal = el('div', { class: 'gp-pal' });
            const fillPal = () => {
                pal.innerHTML = '';
                const q = (P.palQuery || '').toLowerCase();
                const list = P.palette.filter((p) => !q || ((p.label || '') + ' ' + (p.type || '') + ' ' + (p.person_name || '')).toLowerCase().includes(q));
                if (!list.length) { pal.append(el('div', { class: 'muted', style: 'font-size:.8rem;padding:.3rem' }, P.palette.length ? 'Kein Treffer.' : 'Alle Gefährte sind platziert.')); return; }
                list.forEach((p) => pal.append(vehCard(p)));
            };
            search.addEventListener('input', () => { P.palQuery = search.value; fillPal(); });
            palCard.append(search, pal); rail.append(palCard);
            fillPal();
            rail.append(renderVehDetail());
        }
        // Partial update of the Garagenplaner display attributes straight from the detail
        // panel; writes the same vehicle columns as the vehicle form, so both stay in sync.
        async function updateVehPlanner(b, patch) {
            if (!b.vehId) return;
            const body = {
                needs_power: ('needs_power' in patch) ? !!patch.needs_power : !!b.needsPower,
                planner_symbol: ('planner_symbol' in patch) ? (patch.planner_symbol || null) : (b.plannerSymbol || null),
            };
            try {
                await api.put('/vehicles/' + b.vehId + '/planner', body);
                b.needsPower = body.needs_power; b.plannerSymbol = body.planner_symbol;
                const pl = P.palette.find((x) => x.id === b.vehId); if (pl) { pl.needs_power = b.needsPower; pl.planner_symbol = b.plannerSymbol; }
                draw();
            } catch (err) { toast(err.message || 'Speichern fehlgeschlagen', 'error'); }
        }
        function renderVehDetail() {
            const el0 = el('div', { class: 'gp-rcard card' });
            const b = P.spots.find((x) => x._id === P.sel);
            if (!b) { el0.append(el('h3', {}, 'Auswahl'), el('div', { class: 'muted', style: 'font-size:.8rem' }, 'Klick auf ein Gefährt im Plan, oder ein Fahrzeug aus der Liste auf die Fläche ziehen.')); return el0; }
            el0.append(el('h3', {}, (b.type || 'Gefährt') + ' · ' + (b.personName || '')));
            const row = (k, v) => el('div', { class: 'gp-row' }, el('span', { class: 'gp-k' }, k), el('span', { class: 'gp-v num' }, v));
            el0.append(row('Fahrzeug', b.label));
            el0.append(row('Maße', b.L != null && b.W != null && b.H != null ? b.L.toFixed(1) + ' × ' + b.W.toFixed(1) + ' × ' + b.H.toFixed(1) + ' m' : 'offen'));
            el0.append(row('Position', b.x.toFixed(1) + ' / ' + b.y.toFixed(1) + ' m' + (b.rot ? ' · ' + Math.round(b.rot) + '°' : '')));
            if (b.vehId) el0.append(el('button', { class: 'btn btn-ghost btn-sm btn-block', style: 'margin:.4rem 0 .2rem', onclick: () => navigate('vehicles/' + b.vehId) }, '🚗 Fahrzeug-Akte öffnen →'));
            if (b.personId) el0.append(el('button', { class: 'btn btn-ghost btn-sm btn-block', style: 'margin:.2rem 0 .4rem', onclick: () => navigate('persons/' + b.personId) }, '👤 Mieterdaten öffnen →'));
            if (canManageNow) {
                const seg = el('div', { class: 'gp-segd' });
                Object.keys(GPSTAT).forEach((k) => seg.append(el('button', { class: b.status === k ? 'on' : '', onclick: () => { b.status = k; b._dirty = true; markDirty(); pushUndo(); draw(); } }, GPSTAT[k].sym + ' ' + GPSTAT[k].label)));
                el0.append(seg);
                const fit = el('div', { class: 'gp-fit' });
                fit.append(el('h4', {}, el('span', {}, 'Prüfung'), el('span', { class: warn(b) ? 'bad' : 'ok' }, warn(b) ? '⚠ Warnung' : '✓ ok')));
                const line = (t, ok) => el('div', { class: 'gp-line' }, el('span', {}, t), el('span', { class: ok ? 'ok' : 'bad' }, ok ? 'ok' : '!'));
                fit.append(line('Innerhalb der Fläche', inside(b)));
                fit.append(line('Höhe ' + (b.H != null ? b.H.toFixed(1) : '?') + ' m · Tor ' + P.tor.toFixed(1) + ' m', heightOK(b)));
                fit.append(line('Gewicht ' + (b.t != null ? b.t.toFixed(2).replace('.', ',') : '?') + ' ≤ ' + P.load.toFixed(1) + ' t', weightOK(b)));
                el0.append(fit);
                // Per-vehicle buffer opt-out (only meaningful while global buffer is on).
                el0.append(el('button', { class: 'btn btn-ghost btn-sm btn-block', style: 'margin:.15rem 0 .35rem' + (b.noBuf ? '' : ';border-color:var(--gpteal2);color:var(--gpteal2)'), onclick: () => { b.noBuf = !b.noBuf; b._dirty = true; markDirty(); pushUndo(); persistSpot(b); draw(); } }, b.noBuf ? '🛡 Pufferzone: aus' : '🛡 Pufferzone: an'));
                // Planer-Darstellung: Ladebedarf + Symbol pro Fahrzeug — direkt hier (im Sync
                // mit dem Fahrzeug-Formular, da dieselben Fahrzeug-Spalten geschrieben werden).
                if (b.vehId) {
                    const pcard = el('div', { class: 'gp-fit' });
                    pcard.append(el('h4', {}, el('span', {}, 'Planer-Darstellung')));
                    pcard.append(el('button', { class: 'btn btn-ghost btn-sm btn-block', style: 'margin:.15rem 0 .45rem' + (b.needsPower ? ';border-color:var(--gpresv);color:#ffd35b' : ''), onclick: () => updateVehPlanner(b, { needs_power: !b.needsPower }) }, b.needsPower ? '⚡ Ladebedarf: an' : '⚡ Ladebedarf: aus'));
                    const symSel = el('select', { class: 'gp-stepin', 'aria-label': 'Planer-Symbol', onchange: (e) => updateVehPlanner(b, { planner_symbol: e.target.value }) });
                    const symOpts = [['', 'Symbol: Automatisch']].concat(['PKW', 'Motorrad', 'Transporter', 'Wohnmobil', 'Wohnwagen', 'Anhänger', 'Boot / Trailer', 'Traktor', 'Ladewagen', 'Rückewagen', 'Kipper'].map((k) => [k, 'Symbol: ' + k]))
                        .concat((P.plannerIcons || []).map((ic) => ['custom:' + ic.id, 'Eigenes: ' + ic.name]));
                    symOpts.forEach(([v, l]) => { const o = el('option', { value: v }, l); if ((b.plannerSymbol || '') === v) o.selected = true; symSel.append(o); });
                    pcard.append(symSel);
                    // Managing custom icons lives on the toolbar (🖼 Icons) so it's reachable
                    // without selecting a vehicle; here we only pick per-vehicle.
                    el0.append(pcard);
                }
                el0.append(el('div', { class: 'btn-row', style: 'margin-top:.6rem' },
                    b.vehId ? el('button', { class: 'btn btn-ghost btn-sm', onclick: () => dimForm({ id: b.vehId, label: b.label, L: b.L, W: b.W, H: b.H, t: b.t }) }, '✎ Maße') : null,
                    el('button', { class: 'btn btn-ghost btn-sm', onclick: rotateSel }, '⟳ Drehen'),
                    el('button', { class: 'btn btn-ghost btn-sm btn-danger', onclick: () => removeSpot(b) }, 'Entfernen')));
            }
            return el0;
        }
        const SHAPE_ICON = { rect: 'M2 2h36v20H2z', l: 'M2 2h22v10h14v10H2z', u: 'M2 2h9v13h18V2h9v20H2z', trap: 'M8 2h24l6 20H2z', step: 'M2 2h36v9H22v11H2z' };
        function shapeIcon(k) { const s = svgEl('svg', { viewBox: '0 0 40 24', class: 'gp-shapeic' }); s.append(svgEl('path', { d: SHAPE_ICON[k] })); return s; }
        function renderPlanRail() {
            rail.append(hallSwitchCard(true));
            // ---- Werkzeuge: Wände zeichnen / Öffnungen / bearbeiten ----
            const toolsCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Werkzeuge', el('span', { class: 'eyebrow' }, 'Zeichnen')));
            const toolBtn = (kind, label) => el('button', { class: 'btn btn-sm gp-toggle' + (P.tool === kind ? ' on' : ''), onclick: () => setTool(P.tool === kind ? null : kind) }, label);
            toolsCard.append(el('div', { class: 'gp-exgrouplab' }, 'Wände zeichnen (Klick · Klick)'));
            const wrow = el('div', { class: 'gp-addrow' });
            wallKindsList().forEach((k) => wrow.append(toolBtn(k, EXCL[k].label)));
            toolsCard.append(wrow);
            // Wandstärke (Mauerdicke) für NEUE Wände — je Wand später über das Popover änderbar.
            const thSel = el('select', { class: 'gp-stepin', 'aria-label': 'Wandstärke', onchange: (e) => { P.wallThick = e.target.value ? (+e.target.value / 100) : null; } });
            [['', 'Typ-Standard'], ['11.5', '11,5 cm'], ['17.5', '17,5 cm'], ['24', '24 cm'], ['30', '30 cm'], ['36.5', '36,5 cm']].forEach(([v, l]) => { const o = el('option', { value: v }, l); if ((P.wallThick != null ? String(Math.round(P.wallThick * 1000) / 10) : '') === v) o.selected = true; thSel.append(o); });
            toolsCard.append(el('div', { class: 'gp-field', style: 'margin-top:.35rem' }, el('label', {}, 'Wandstärke (neue Wände)'), thSel));
            // Bezug: is the drawn line the Innen(kante), Achse (Mitte) or Außen(kante) der Wand?
            const refSeg = el('div', { class: 'gp-seg', style: 'margin-top:.15rem' });
            [['inner', 'Innen'], ['axis', 'Achse'], ['outer', 'Außen']].forEach(([k, l]) => refSeg.append(el('button', { class: P.wallRef === k ? 'on' : '', title: 'Bezug der gezeichneten Linie — bei „Innen" ist der Umriss dein lichtes Innenmaß und m² stimmt 1:1', onclick: () => { P.wallRef = k; _eoffKey = null; draw(); } }, l)));
            toolsCard.append(el('div', { class: 'gp-field', style: 'margin-top:.35rem' }, el('label', {}, 'Bezug der Zeichenlinie'), refSeg));
            toolsCard.append(el('div', { class: 'gp-exgrouplab' }, 'Öffnungen (auf eine Wand)'));
            const orow = el('div', { class: 'gp-addrow' });
            openKindsList().forEach((k) => orow.append(toolBtn(k, EXCL[k].label)));
            toolsCard.append(orow);
            toolsCard.append(el('div', { class: 'gp-addrow', style: 'margin-top:.4rem' },
                el('button', { class: 'btn btn-sm gp-toggle btn-block' + (!P.tool ? ' on' : ''), title: 'Normale Maus: auswählen, verschieben, bearbeiten', onclick: () => setTool(null) }, '↖ Auswahl / Bearbeiten')));
            toolsCard.append(el('div', { class: 'muted', style: 'font-size:.72rem;margin-top:.4rem' },
                (P.tool && isWallDraw(P.tool)) ? 'Klick setzt Punkte, jede weitere Ecke zeichnet weiter. Startpunkt klicken oder Doppelklick beendet. Auf eine bestehende Wand klicken = Anbau. Rechtsklick/Esc = Werkzeug ablegen.'
                    : (P.tool && isOpenKind(P.tool)) ? 'Auf eine Wand klicken (Start), Breite ziehen, zweiter Klick setzt die Öffnung. Rechtsklick/Esc = Werkzeug ablegen.'
                        : (P.tool && (isZoneTool(P.tool) || P.tool === 'column')) ? (P.tool === 'column' ? 'Klicken setzt eine Stütze (oder aufziehen für die Größe). Rechtsklick/Esc = ablegen.' : 'Ins Canvas klicken und die Fläche aufziehen. Rechtsklick/Esc = Werkzeug ablegen.')
                            : 'Auswahl-Modus: Objekt anklicken zum Bearbeiten (Wände: Knoten ziehen, Segment doppelklicken = Punkt einfügen). Löschen nur über 🗑 am Objekt oder Entf.'));
            const grid2 = el('div', { class: 'gp-grid2', style: 'margin-top:.5rem' });
            const dstep = (label, which, val, step) => el('div', { class: 'gp-field' }, el('label', {}, label),
                el('div', { class: 'gp-stepper' }, el('button', { onclick: () => setDim(which, -1) }, '−'),
                    el('input', { class: 'gp-stepin num', type: 'number', step, value: val, onchange: (e) => setDimTo(which, e.target.value) }),
                    el('button', { onclick: () => setDim(which, 1) }, '+')));
            grid2.append(dstep('Torhöhe (m)', 'tor', P.tor.toFixed(1), '0.5'), dstep('Bodenlast (t)', 'load', P.load.toFixed(1), '0.5'));
            toolsCard.append(grid2); rail.append(toolsCard);
            // Functional zones (Fahrstraße / Wartung / Notausgang / Stellfläche) — placed as
            // rectangles. Walls & openings are drawn interactively above, not added here.
            const exCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Flächen, Zonen & Bauteile'));
            const zrow = el('div', { class: 'gp-addrow' });
            Object.entries(EXCL).filter(([, m]) => m.cat === 'zone').forEach(([k, m]) => zrow.append(el('button', { class: 'btn btn-sm gp-toggle' + (P.tool === k ? ' on' : ''), title: 'Im Plan aufziehen', onclick: () => setTool(P.tool === k ? null : k) }, m.label)));
            zrow.append(el('button', { class: 'btn btn-sm gp-toggle' + (P.tool === 'column' ? ' on' : ''), title: 'Stütze setzen (klicken oder aufziehen)', onclick: () => setTool(P.tool === 'column' ? null : 'column') }, EXCL.column.label));
            exCard.append(zrow);
            exCard.append(el('div', { class: 'muted', style: 'font-size:.72rem;margin:.1rem 0 .3rem' }, 'Wählen und im Plan aufziehen (Stütze: klicken genügt); danach Ecken ziehen, ⟳ oben = drehen, 🗑 am Objekt löscht.'));
            const exList = el('div', { class: 'gp-exlist' });
            if (!P.excl.length) exList.append(el('div', { class: 'muted', style: 'font-size:.76rem;margin-top:.4rem' }, 'Noch keine Flächen.'));
            P.excl.forEach((b) => {
                const it = el('div', { class: 'gp-exitem' + (b.id === P.sel ? ' on' : '') });
                const sw = el('span', { class: 'gp-sw' }); sw.dataset.kind = b.kind;
                const meta = (isWallKind(b.kind) && b.mat && MAT[b.mat]) ? ' · ' + MAT[b.mat] : '';
                it.append(sw, el('span', { style: 'flex:1' }, b.label, ' ', el('span', { class: 'muted num', style: 'font-size:.72rem' }, b.w + '×' + b.h + ' m' + meta)),
                    el('button', { class: 'gp-rm', title: 'Entfernen', onclick: (e) => { e.stopPropagation(); removeExcl(b); } }, '×'));
                it.addEventListener('click', () => { P.sel = b.id; draw(); });
                exList.append(it);
            });
            exCard.append(exList);
            const b = P.excl.find((x) => x.id === P.sel);
            if (b) {
                // Type switcher — sibling kinds within the selected block's own category.
                const cat = (EXCL[b.kind] && EXCL[b.kind].cat) || 'zone';
                const segd = el('div', { class: 'gp-segd', style: 'margin-top:.6rem' });
                Object.entries(EXCL).filter(([, m]) => (m.cat || 'zone') === cat).forEach(([k, m]) => segd.append(el('button', { class: b.kind === k ? 'on' : '', onclick: () => { b.kind = k; b.label = EXCL[k].label; if (isWallKind(k) && !b.mat) b.mat = EXCL[k].mat; if (!isWallKind(k)) b.mat = undefined; commitGeom(); } }, m.label)));
                exCard.append(segd);
                // Material picker (Wände/Stütze only).
                if (isWallKind(b.kind)) {
                    const matSel = el('select', { class: 'gp-stepin', 'aria-label': 'Material', onchange: (e) => { b.mat = e.target.value || undefined; commitGeom(); } });
                    Object.entries(MAT).forEach(([mk, ml]) => { const o = el('option', { value: mk }, ml); if ((b.mat || '') === mk) o.selected = true; matSel.append(o); });
                    exCard.append(el('div', { class: 'gp-field', style: 'margin-top:.5rem' }, el('label', {}, 'Material'), matSel));
                }
                // Direct size entry (Breite × Tiefe) for the selected block.
                const sizeRow = el('div', { class: 'gp-grid2', style: 'margin-top:.5rem' },
                    el('div', { class: 'gp-field' }, el('label', {}, isWallKind(b.kind) ? 'Länge (m)' : 'Breite (m)'), el('input', { class: 'gp-stepin num', type: 'number', step: '0.1', value: b.w, onchange: (e) => setExclSize(b, 'w', e.target.value) })),
                    el('div', { class: 'gp-field' }, el('label', {}, isWallKind(b.kind) ? 'Dicke (m)' : 'Tiefe (m)'), el('input', { class: 'gp-stepin num', type: 'number', step: isWallKind(b.kind) ? '0.01' : '0.1', value: b.h, onchange: (e) => setExclSize(b, 'h', e.target.value) })));
                exCard.append(sizeRow, el('div', { class: 'muted', style: 'font-size:.72rem;margin-top:.35rem' }, 'Ecke ziehen = Größe · Ziehen = verschieben.'));
            }
            rail.append(exCard);
            // ---- Parkfläche (automatisch aus den Wänden) ----
            const areaCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Parkfläche', el('span', { class: 'eyebrow' }, 'aus Wänden')));
            areaCard.append(el('button', { class: 'btn btn-sm gp-toggle btn-block' + (P.autoArea ? ' on' : ''), onclick: () => { P.autoArea = !P.autoArea; draw(); renderRail(); } }, P.autoArea ? '👁 Automatisch: an' : '⦸ Automatisch: aus'));
            if (P.autoArea) {
                const enc0 = enclosure();
                if (enc0 && enc0.area > 0.3) {
                    areaCard.append(el('div', { class: 'muted', style: 'font-size:.75rem;margin:.45rem 0' }, 'Umschlossen erkannt: ' + enc0.area.toFixed(1).replace('.', ',') + ' m² (grün schraffiert).'));
                    areaCard.append(el('button', { class: 'btn btn-sm btn-block', title: 'Den erkannten Umriss als Hallenfläche übernehmen', onclick: deriveFloorFromWalls }, '⤵ Umriss aus Wänden übernehmen'));
                } else {
                    areaCard.append(el('div', { class: 'muted', style: 'font-size:.75rem;margin:.45rem 0' }, 'Außenwände zu einem geschlossenen Ring zeichnen — die Parkfläche wird dann automatisch erkannt und schraffiert.'));
                }
            }
            rail.append(areaCard);
            // ---- Bauplan (Unterlage) ----
            const planCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Bauplan', el('span', { class: 'eyebrow' }, 'Unterlage')));
            const fileIn = el('input', { type: 'file', accept: 'image/*', style: 'display:none', onchange: (e) => { loadBauplan(e.target.files && e.target.files[0]); e.target.value = ''; } });
            planCard.append(fileIn, el('div', { class: 'gp-addrow' }, el('button', { class: 'btn btn-sm', onclick: () => fileIn.click() }, P.plan ? '↻ Bild ersetzen' : '⤒ Bauplan laden')));
            if (P.plan) {
                const showBtn = el('button', { class: 'btn btn-sm gp-toggle' + (!P.plan.hidden ? ' on' : ''), onclick: () => { P.plan.hidden = !P.plan.hidden; markDirty(); draw(); renderRail(); } }, P.plan.hidden ? '⦸ Ausgeblendet' : '👁 Sichtbar');
                const calBtn = el('button', { class: 'btn btn-sm gp-toggle' + (P.calib ? ' on' : ''), onclick: () => { P.calib = !P.calib; draw(); renderRail(); } }, '⤡ Kalibrieren');
                const op = el('input', { type: 'range', min: '10', max: '100', value: String(Math.round(P.plan.opacity * 100)), 'aria-label': 'Deckkraft', oninput: (e) => { P.plan.opacity = (+e.target.value) / 100; markDirty(); drawFloor(); } }); op.style.width = '100%';
                planCard.append(el('div', { style: 'margin-top:.5rem;display:flex;flex-direction:column;gap:.45rem' },
                    el('div', { class: 'gp-addrow' }, showBtn, calBtn),
                    el('div', { class: 'gp-field' }, el('label', {}, 'Deckkraft'), op),
                    el('div', { class: 'gp-addrow' },
                        el('button', { class: 'btn btn-sm', title: 'Auf die Hallenfläche einpassen', onclick: () => { const bb = floorBB(); P.plan.x = bb.minX; P.plan.y = bb.minY; P.plan.w = Math.max(1, bb.maxX - bb.minX); P.plan.h = Math.max(1, bb.maxY - bb.minY); markDirty(); draw(); } }, '⤢ An Halle anpassen'),
                        el('button', { class: 'btn btn-sm btn-danger', onclick: () => { P.plan = null; P.calib = false; markDirty(); draw(); renderRail(); toast('Bauplan entfernt'); } }, 'Entfernen'))));
                planCard.append(el('div', { class: 'muted', style: 'font-size:.72rem;margin-top:.4rem' }, 'Kalibrieren: Bauplan verschieben und an den Ecken auf Maßstab ziehen. Wände/Fahrzeuge lassen sich dann exakt darüber platzieren.'));
            } else {
                planCard.append(el('div', { class: 'muted', style: 'font-size:.72rem;margin-top:.4rem' }, 'Ein Foto/Scan des Grundrisses als Unterlage laden, kalibrieren und darüber planen.'));
            }
            rail.append(planCard);
        }
        function setDimTo(which, value) {
            const v = Number(value); if (!Number.isFinite(v) || value === '' || v <= 0) return;
            if (which === 'w') { P.Wm = Math.round(Math.max(6, Math.min(40, v))); P.floor = gpShape(P.shape, P.Wm, P.Hm); clampAll(); }
            else if (which === 'h') { P.Hm = Math.round(Math.max(5, Math.min(30, v))); P.floor = gpShape(P.shape, P.Wm, P.Hm); clampAll(); }
            else if (which === 'tor') P.tor = Math.max(1.5, Math.min(8, Math.round(v * 10) / 10));
            else if (which === 'load') P.load = Math.max(0.5, Math.min(60, Math.round(v * 10) / 10));
            hideLen(); pushUndo(); markDirty(); layout();
        }
        function setExclSize(b, dim, value) {
            const v = Number(value); if (!Number.isFinite(v) || v <= 0) return;
            const nv = Math.max(0.2, Math.min(dim === 'w' ? P.Wm : P.Hm, Math.round(v * 100) / 100));
            const cand = { kind: 'excl', x: b.x, y: b.y, w: dim === 'w' ? nv : b.w, h: dim === 'h' ? nv : b.h };
            if (b.x + cand.w > P.Wm || b.y + cand.h > P.Hm || (!isZoneKind(b.kind) && collide(cand, b.id))) { toast('Passt nicht (Rand/Kollision)', 'error'); draw(); return; }
            b[dim] = nv;
            // Clip flush to a nearby wall the same way dragging does: typing a size that lands within
            // snap reach of a wall face (e.g. a depth that leaves a half-thickness gap at a gate) now
            // snaps to the true inner surface instead of stopping short. Re-clamp to stay in bounds.
            snapZoneFaces(b);
            b.x = Math.max(0, Math.min(P.Wm - b.w, b.x)); b.y = Math.max(0, Math.min(P.Hm - b.h, b.y));
            commitGeom();
        }
        function setShape(k) { P.shape = k; P.floor = gpShape(k, P.Wm, P.Hm); hideLen(); const out = P.spots.filter((s) => !inside(s)).length; commitGeom('Form: ' + k + (out ? ' · ' + out + ' außerhalb' : ''), out ? 'warn' : ''); }
        function setDim(which, d) {
            if (which === 'w') { P.Wm = Math.max(6, Math.min(40, P.Wm + d)); P.floor = gpShape(P.shape, P.Wm, P.Hm); clampAll(); }
            else if (which === 'h') { P.Hm = Math.max(5, Math.min(30, P.Hm + d)); P.floor = gpShape(P.shape, P.Wm, P.Hm); clampAll(); }
            else if (which === 'tor') P.tor = Math.max(1.5, Math.min(8, +(P.tor + d * 0.5).toFixed(1)));
            else if (which === 'load') P.load = Math.max(0.5, Math.min(60, +(P.load + d * 0.5).toFixed(1)));
            hideLen(); pushUndo(); markDirty(); layout();
        }
        function clampAll() { P.spots.concat(P.excl).forEach((t) => { const cl = clampXY(t, t.x, t.y); t.x = cl.x; t.y = cl.y; if (t._id) t._dirty = true; }); }
        function addExcl(k) {
            const s = EXCL[k]; const b = { id: 'e' + (P.uid++), kind: k, x: 1, y: 1, w: s.w, h: s.h, label: s.label, mat: s.mat };
            // Zones (Stellfläche) may sit anywhere; blocking structures seek a free cell.
            if (!s.zone) outer: for (let gy = 0; gy < P.Hm - s.h + 1; gy++) { for (let gx = 0; gx < P.Wm - s.w + 1; gx++) { b.x = gx; b.y = gy; if (!collide(b, null)) break outer; } }
            P.excl.push(b); P.sel = b.id; commitGeom(s.label + ' hinzugefügt');
        }
        function removeExcl(b) { P.excl = P.excl.filter((x) => x !== b); if (P.sel === b.id) P.sel = null; commitGeom('Fläche entfernt'); }

        async function removeSpot(b) {
            try { await api.del('/spots/' + b._id); P.spots = P.spots.filter((x) => x !== b);
                const pv = { id: b.vehId, label: b.label, type: b.type, person_id: b.personId, person_name: b.personName, length_m: b.L, width_m: b.W, height_m: b.H, weight_t: b.t };
                if (b.vehId) P.palette.push(pv);
                if (P.sel === b._id) P.sel = null; draw(); toast('Gefährt entfernt'); }
            catch (err) { toast(err.message || 'Entfernen fehlgeschlagen', 'error'); }
        }

        // ---- mode / maximize ----
        function setMode(m) { P.mode = m; root.dataset.mode = m; P.sel = null; selSet = []; P.calib = false; P.tool = null; P.chain = null; P.openStart = null; P.preview = null; P.structSel = null; P.chainAnchor = null; P.guide = null; P.attach = null; hideLen(); hideVertMenu(); ctitle.textContent = m === 'plan' ? 'Garagenplaner' : 'Digitaler Zwilling'; rebuildSwitch(); draw(); }
        // Re-fit on enter AND exit so the plan fills whichever viewport we land in (entering
        // fullscreen used to keep the small windowed scale → a tiny plan marooned in a huge canvas).
        function toggleMax(force) { P.maxed = force == null ? !P.maxed : force; root.classList.toggle('maxed', P.maxed); maxBtn.textContent = P.maxed ? '⤢' : '⛶'; if (!P.maxed) { rail.style.left = ''; rail.style.top = ''; rail.style.right = ''; } setTimeout(() => { if (P.walls.edges.length) fitView(); else layout(); }, 20); }
        // In fullscreen the rail floats over the canvas and can be dragged by any
        // card header ("dynamisch drüberliegend und verschiebbar").
        let railDrag = null;
        rail.addEventListener('pointerdown', (e) => { if (!P.maxed) return; const h = e.target.closest('.gp-rcard h3'); if (!h) return; const r = rail.getBoundingClientRect(); railDrag = { dx: e.clientX - r.left, dy: e.clientY - r.top }; try { rail.setPointerCapture(e.pointerId); } catch (er) { /* ignore */ } e.preventDefault(); });
        rail.addEventListener('pointermove', (e) => { if (!railDrag) return; rail.style.left = Math.max(4, e.clientX - railDrag.dx) + 'px'; rail.style.top = Math.max(4, e.clientY - railDrag.dy) + 'px'; rail.style.right = 'auto'; });
        rail.addEventListener('pointerup', (e) => { if (railDrag) { railDrag = null; try { rail.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ } } });

        // ---- floor editing (vertices/edges) ----
        // (legacy floor-polygon vertex/edge svg editor removed — walls define the plan now)

        // ---- block move / resize / select ----
        let act = null;
        layer.addEventListener('pointerdown', (e) => {
            if (e.button !== 0 || spaceDown) return; // Space held → let planWrap pan
            P.roomSel = null; // any plan click clears the room badge; an empty click re-shows it
            const rt = e.target.closest('.gp-rot');
            if (rt && canManageNow) { const bb = P.spots.find((x) => x._id == rt.dataset.rotid) || P.excl.find((x) => x.id === rt.dataset.rotid); if (bb) { act = { mode: 'rotate', b: bb, elm: e.target.closest('.gp-block'), cx: bb.x + bb.w / 2, cy: bb.y + bb.h / 2, orot: bb.rot || 0 }; e.target.setPointerCapture(e.pointerId); } return; }
            const rz = e.target.closest('.gp-rz');
            if (rz && canManageNow) { const id = rz.dataset.rz; const bb = P.spots.find((x) => x._id == id) || P.excl.find((x) => x.id === id); if (bb) { act = { mode: 'resize', b: bb, elm: e.target.closest('.gp-block'), dir: rz.dataset.dir, o0: { x: bb.x, y: bb.y, w: bb.w, h: bb.h, rot: bb.rot || 0 } }; e.target.setPointerCapture(e.pointerId); } return; }
            const elm = e.target.closest('.gp-block');
            if (!elm) { // empty floor: begin a marquee (rubber-band multi-select) — resolves to a click if it doesn't drag
                const r = planEl.getBoundingClientRect();
                marq = { sx: e.clientX, sy: e.clientY, x0: (e.clientX - r.left) / P.CELL, y0: (e.clientY - r.top) / P.CELL, add: e.shiftKey || e.ctrlKey || e.metaKey, moved: false, el: null };
                layer.setPointerCapture(e.pointerId); return;
            }
            const id = elm.dataset.id; const b = P.spots.find((x) => x._id == id) || P.excl.find((x) => x.id === id); if (!b) return;
            if (!interactive(b) || !canManageNow) { P.sel = b._id || b.id; if (!inSel(b._id || b.id)) clearSelSet(); draw(); return; }
            const r = planEl.getBoundingClientRect(), myId = b._id || b.id;
            // grabbing one of several selected objects → move the whole group; otherwise single (and drop any group)
            if (inSel(myId) && selSet.length > 1) {
                const members = selSet.map(findBlock).filter((x) => x && interactive(x)).map((x) => ({ b: x, ox: x.x, oy: x.y }));
                act = { mode: 'move', group: members, b, elm, sx: e.clientX, sy: e.clientY, gx: e.clientX - r.left - b.x * P.CELL, gy: e.clientY - r.top - b.y * P.CELL, moved: false, ox: b.x, oy: b.y };
            } else {
                if (clearSelSet()) draw();
                act = { mode: 'move', b, elm, sx: e.clientX, sy: e.clientY, gx: e.clientX - r.left - b.x * P.CELL, gy: e.clientY - r.top - b.y * P.CELL, moved: false, ox: b.x, oy: b.y };
            }
            elm.setPointerCapture(e.pointerId);
        });
        layer.addEventListener('pointermove', (e) => {
            if (marq) {
                if (!marq.moved && Math.abs(e.clientX - marq.sx) + Math.abs(e.clientY - marq.sy) < 4) return;
                marq.moved = true; const r = planEl.getBoundingClientRect();
                if (!marq.el) { marq.el = el('div', { class: 'gp-marquee' }); planEl.append(marq.el); }
                const x1 = (e.clientX - r.left) / P.CELL, y1 = (e.clientY - r.top) / P.CELL;
                const lo = { x: Math.min(marq.x0, x1), y: Math.min(marq.y0, y1) }, hi = { x: Math.max(marq.x0, x1), y: Math.max(marq.y0, y1) };
                marq.el.style.left = (lo.x * P.CELL) + 'px'; marq.el.style.top = (lo.y * P.CELL) + 'px';
                marq.el.style.width = ((hi.x - lo.x) * P.CELL) + 'px'; marq.el.style.height = ((hi.y - lo.y) * P.CELL) + 'px';
                marq.box = { lo, hi }; return;
            }
            if (!act) return; const r = planEl.getBoundingClientRect(), b = act.b;
            if (act.mode === 'rotate') {
                const wx = (e.clientX - r.left) / P.CELL, wy = (e.clientY - r.top) / P.CELL;
                let ang = Math.atan2(wy - act.cy, wx - act.cx) * 180 / Math.PI + 90; // handle sits above centre
                if (P.snap && !e.altKey) ang = Math.round(ang / 15) * 15; // 15° snap unless Alt = free
                ang = ((ang % 360) + 360) % 360; b.rot = ang;
                act.elm.style.transform = 'rotate(' + ang + 'deg)';
                act.elm.querySelectorAll('.gp-code,.gp-lab,.gp-warnb').forEach((t) => { t.style.transform = 'rotate(' + (-ang) + 'deg)'; }); // keep labels upright while dragging
                act.elm.classList.toggle('invalid', b._id ? !validVeh(b, b._id) : (isZoneKind(b.kind) ? false : collide(b, b.id))); return;
            }
            if (act.mode === 'resize') {
                const px = (e.clientX - r.left) / P.CELL, py = (e.clientY - r.top) / P.CELL;
                const nr = resizeBlock(act.o0, act.dir, px, py); b.x = nr.x; b.y = nr.y; b.w = nr.w; b.h = nr.h;
                positionBlock(act.elm, b); // left/top/width/height (+ rotation transform)
                const isVeh = !!b._id && b.kind !== 'excl';
                if (isVeh) { b.L = b.w; b.W = b.h; act.elm.querySelectorAll('.gp-dim,.gp-rl-dim').forEach((t) => { t.textContent = dimText(b); }); } // live L×B
                const bad = isVeh ? !validVeh({ kind: 'veh', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b._id) : (isZoneKind(b.kind) ? false : collide({ kind: 'excl', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b.id));
                act.elm.classList.toggle('invalid', bad); renderMetrics(); return; }
            if (!act.moved && Math.abs(e.clientX - act.sx) + Math.abs(e.clientY - act.sy) < 4) return; act.moved = true; act.elm.classList.add('moving');
            let nx = (e.clientX - r.left - act.gx) / P.CELL, ny = (e.clientY - r.top - act.gy) / P.CELL; nx = gsnap(nx); ny = gsnap(ny);
            const cl = snapPos(b, nx, ny); nx = cl.x; ny = cl.y; b.x = nx; b.y = ny; act.elm.style.left = (nx * P.CELL) + 'px'; act.elm.style.top = (ny * P.CELL) + 'px';
            if (act.group) { const dx = nx - act.ox, dy = ny - act.oy; // rigid group move: shift every member by the same (snapped) delta
                act.group.forEach((m) => { if (m.b === b) return; m.b.x = m.ox + dx; m.b.y = m.oy + dy; const me = layer.querySelector('.gp-block[data-id="' + (m.b._id || m.b.id) + '"]'); if (me) { me.style.left = (m.b.x * P.CELL) + 'px'; me.style.top = (m.b.y * P.CELL) + 'px'; } }); }
            const isVeh = !!b._id && b.kind !== 'excl'; const bad = isVeh ? !validVeh({ kind: 'veh', x: nx, y: ny, w: b.w, h: b.h, rot: b.rot }, b._id) : (isZoneKind(b.kind) ? false : collide({ kind: 'excl', x: nx, y: ny, w: b.w, h: b.h }, b.id)); act.elm.classList.toggle('invalid', bad);
        });
        layer.addEventListener('pointerup', (e) => {
            if (marq) {
                const m = marq; marq = null; try { layer.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ } if (m.el) m.el.remove();
                if (!m.moved) { const changed = clearSelSet(); P.roomSel = null; showRoom({ x: m.x0, y: m.y0 }); draw(); return; } // plain click → deselect + room m²
                const box = m.box || { lo: { x: m.x0, y: m.y0 }, hi: { x: m.x0, y: m.y0 } }, hits = [];
                const consider = (arr) => arr.forEach((bl) => { if (!interactive(bl)) return; const cx = bl.x + bl.w / 2, cy = bl.y + bl.h / 2; if (cx >= box.lo.x && cx <= box.hi.x && cy >= box.lo.y && cy <= box.hi.y) hits.push(bl._id || bl.id); });
                consider(P.spots); consider(P.excl); // interactive() already scopes to zones (plan) OR vehicles (manage)
                if (!m.add) selSet = []; hits.forEach((id) => { if (selSet.indexOf(id) < 0) selSet.push(id); });
                P.sel = null; P.roomSel = null; draw(); if (selSet.length) toast(selSet.length + ' ausgewählt · ziehen zum Verschieben · Entf zum Löschen', 'ok');
                return;
            }
            if (!act) return; const b = act.b, d = act; act = null;
            try { d.elm.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ }
            const isVeh = !!b._id && b.kind !== 'excl';
            if (d.mode === 'rotate') {
                const bad = isVeh ? !validVeh(b, b._id) : (isZoneKind(b.kind) ? false : collide(b, b.id));
                if (bad && !isVeh) { b.rot = d.orot; draw(); toast('Drehen: Kollision/außerhalb — zurück', 'error'); return; } // exclusions still snap back
                P.sel = b._id || b.id;
                if (isVeh) { b._invalid = bad; b._dirty = true; markDirty(); pushUndo(); draw(); toast(bad ? 'Gedreht — ungültige Lage, Speichern erst wenn frei' : 'Gedreht', bad ? 'warn' : ''); }
                else { commitGeom(); toast('Gedreht'); } // commitGeom already redraws
                return;
            }
            if (d.mode === 'resize') {
                const bad = isVeh ? !validVeh({ kind: 'veh', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b._id) : (isZoneKind(b.kind) ? false : collide({ kind: 'excl', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b.id));
                if (bad) { b.x = d.o0.x; b.y = d.o0.y; b.w = d.o0.w; b.h = d.o0.h; if (isVeh) { b.L = d.o0.w; b.W = d.o0.h; } P.sel = b._id || b.id; draw(); toast('Passt nicht (Rand/Kollision) — zurück', 'error'); return; }
                P.sel = b._id || b.id;
                if (isVeh) { b.L = b.w; b.W = b.h; persistDims(b); b._dirty = true; markDirty(); pushUndo(); } else commitGeom();
                draw(); toast('Größe geändert' + (isVeh ? ' · ' + dimText(b).replace(/^.*· /, '') : '')); return;
            }
            if (!d.moved) { P.sel = b._id || b.id; draw(); return; }
            if (d.group) { // group move commit — rigid shift keeps inter-member validity, so evaluate each like a single move
                d.elm.classList.remove('moving');
                const res = d.group.map((m) => { const mb = m.b, mv = !!mb._id && mb.kind !== 'excl'; const bd = mv ? !validVeh({ kind: 'veh', x: mb.x, y: mb.y, w: mb.w, h: mb.h, rot: mb.rot }, mb._id) : (isZoneKind(mb.kind) ? false : collide({ kind: 'excl', x: mb.x, y: mb.y, w: mb.w, h: mb.h }, mb.id)); return { mv, bd }; });
                if (res.some((x) => !x.mv && x.bd)) { d.group.forEach((m) => { m.b.x = m.ox; m.b.y = m.oy; }); draw(); toast('Gruppe: Kollision/außerhalb — zurück', 'error'); return; }
                res.forEach((x, i) => { if (x.mv) { d.group[i].b._invalid = x.bd; d.group[i].b._dirty = true; } });
                markDirty(); pushUndo(); draw(); const bad = res.some((x) => x.bd);
                toast(d.group.length + ' verschoben' + (bad ? ' · einige ungültig, Speichern erst wenn frei' : ''), bad ? 'warn' : 'ok'); return;
            }
            d.elm.classList.remove('moving');
            const bad = isVeh ? !validVeh({ kind: 'veh', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b._id) : (isZoneKind(b.kind) ? false : collide({ kind: 'excl', x: b.x, y: b.y, w: b.w, h: b.h }, b.id));
            P.sel = b._id || b.id;
            if (!isVeh) { // exclusions still snap back to their last valid spot
                if (bad) { b.x = d.ox; b.y = d.oy; toast('Kollision/außerhalb — zurück', 'error'); draw(); }
                else { commitGeom(); toast('Verschoben' + (P.snap ? ' (gerastet)' : ' (frei)')); }
                return;
            }
            // vehicle: keep the position even when invalid; persist only once it is valid again
            b._invalid = bad; b._dirty = true; markDirty(); pushUndo(); draw();
            toast(bad ? 'Außerhalb/blockiert — Position bleibt, Speichern erst wenn gültig' : ('Verschoben' + (P.snap ? ' (gerastet)' : ' (frei)')), bad ? 'warn' : '');
        });

        // ---- interactive wall drawing / node editing (overlay in plan mode) ----
        function setTool(kind) { P.tool = kind || null; P.chain = null; P.openStart = null; P.preview = null; P.snapHint = null; P.guide = null; P.attach = null; P.chainAnchor = null; if (P.tool) { P.structSel = null; } draw(); }
        function evWorld(ev) { const r = planEl.getBoundingClientRect(); return { x: (ev.clientX - r.left) / P.CELL, y: (ev.clientY - r.top) / P.CELL }; }
        // Snap a drawing point: existing node > point on an existing wall (Anbau, records the
        // attach split for the distance readout) > grid, with automatic orthogonal (H/V) snap
        // to the chain's last point within an ~8px tolerance (or hard-locked via ⊾90°/Shift).
        function snapDraw(p, shift) {
            const R = 12 / P.CELL, tol = 8 / P.CELL; P.snapHint = null; P.guide = null; P.attach = null;
            const ni = nodeAt(p, R); if (ni >= 0) { const n = P.walls.nodes[ni]; P.snapHint = { x: n.x, y: n.y }; return { x: n.x, y: n.y, node: ni }; }
            const e = edgeAt(p, R); if (e) { P.snapHint = { x: e.x, y: e.y }; P.attach = { ei: e.i, t: e.t, x: e.x, y: e.y }; return { x: e.x, y: e.y, edge: e.i, t: e.t }; }
            let x = gsnap(p.x), y = gsnap(p.y);
            if (P.chain && P.chain.length) {
                const last = P.walls.nodes[P.chain[P.chain.length - 1]];
                if (last) { const dX = Math.abs(x - last.x), dY = Math.abs(y - last.y), hard = P.ortho || shift;
                    if (hard) { if (dX < dY) { x = last.x; P.guide = { kind: 'v', x }; } else { y = last.y; P.guide = { kind: 'h', y }; } }
                    else if (dY <= tol && dY <= dX) { y = last.y; P.guide = { kind: 'h', y }; }
                    else if (dX <= tol) { x = last.x; P.guide = { kind: 'v', x }; }
                }
            }
            return { x, y };
        }
        function endChain(msg) { P.chain = null; P.preview = null; P.snapHint = null; P.guide = null; P.attach = null; P.chainAnchor = null; pruneNodes(); refreshFloorFromWalls(); if (msg) toast(msg, 'ok'); equalizePadding(); }
        function wallClick(sp) {
            let ni, changed = false;
            if (sp.node != null) ni = sp.node;
            else if (sp.edge != null) {
                // Anbau: record the attach point's distances to the ORIGINAL wall's ends first
                if (!P.chain) { const v0 = edgeVec(P.walls.edges[sp.edge]); P.chainAnchor = { x: sp.x, y: sp.y, aEnd: { x: v0.a.x, y: v0.a.y }, bEnd: { x: v0.b.x, y: v0.b.y } }; }
                ni = splitEdgeAt(sp.edge, sp.t); changed = true;
            }
            else ni = addNode(sp.x, sp.y);
            if (!P.chain) { P.chain = [ni]; if (changed) { refreshFloorFromWalls(); pushUndo(); markDirty(); } draw(); return; }
            const lastNi = P.chain[P.chain.length - 1]; if (ni === lastNi) return;
            addEdge(lastNi, ni, P.tool); P.chain.push(ni); refreshFloorFromWalls(); pushUndo(); markDirty();
            if (ni === P.chain[0]) { endChain('Raum geschlossen'); return; }
            layout();
        }
        function openClick(p) {
            const R = 14 / P.CELL;
            if (!P.openStart) { const e = edgeAt(p, R); if (!e) { toast('Auf eine Wand klicken', 'warn'); return; } P.openStart = { ei: e.i, t: e.t }; P.preview = { x: e.x, y: e.y, t: e.t }; draw(); return; }
            const ed = P.walls.edges[P.openStart.ei]; if (!ed) { P.openStart = null; P.preview = null; return; }
            const v = edgeVec(ed); let t2 = ((p.x - v.a.x) * v.dx + (p.y - v.a.y) * v.dy) / (v.len * v.len); t2 = Math.max(0, Math.min(1, t2));
            const c = (P.openStart.t + t2) / 2, w = Math.abs(t2 - P.openStart.t) * v.len;
            P.openStart = null; P.preview = null;
            if (w < 0.2) { toast('Öffnung zu schmal', 'warn'); draw(); return; }
            // reject an opening that would overlap an existing one on the same wall (S3-C)
            const lo = c - w / (2 * v.len), hi = c + w / (2 * v.len);
            if ((ed.ops || []).some((x) => { const xh = x.c + x.w / (2 * v.len), xl = x.c - x.w / (2 * v.len); return lo < xh - 1e-3 && hi > xl + 1e-3; })) { toast('Öffnung überlappt eine bestehende', 'warn'); draw(); return; }
            ed.ops = ed.ops || []; ed.ops.push({ c, w, kind: P.tool }); pushUndo(); markDirty(); draw(); toast(EXCL[P.tool].label + ' eingefügt', 'ok');
        }
        function editDown(ev, p) {
            const R = 12 / P.CELL, cap = () => { try { drawOverlay.setPointerCapture(ev.pointerId); } catch (er) { /* ignore */ } };
            P.roomSel = null; // any click clears the room badge (empty-room click re-shows it below)
            // resize handle of the already-selected opening wins
            if (P.structSel && P.structSel.type === 'opening') { const h = openingHandleAt(p, P.structSel.ei, P.structSel.oi, R); if (h) { opDrag = { ei: P.structSel.ei, oi: P.structSel.oi, mode: 'resize', end: h, moved: false }; cap(); return; } }
            // opening body → select + move along the wall
            const op = openingAt(p, R);
            if (op) { const e = P.walls.edges[op.ei], v = edgeVec(e), o = e.ops[op.oi], t = ((p.x - v.a.x) * v.dx + (p.y - v.a.y) * v.dy) / (v.len * v.len); P.structSel = { type: 'opening', ei: op.ei, oi: op.oi }; P.sel = null; opDrag = { ei: op.ei, oi: op.oi, mode: 'move', grab: t - o.c, moved: false }; cap(); draw(); return; }
            const ni = nodeJointAt(p, R);
            if (ni >= 0) { P.structSel = { type: 'node', idx: ni }; P.sel = null; nodeDrag = { ni, moved: false, gx: P.walls.nodes[ni].x - p.x, gy: P.walls.nodes[ni].y - p.y }; cap(); draw(); return; }
            const e = edgeAt(p, R);
            if (e) { P.structSel = { type: 'edge', idx: e.i }; P.sel = null; draw(); return; }
            // rotate knob of the currently-selected zone (matches the layer's .gp-rot at top:-22px)
            if (P.sel != null) { const b = P.excl.find((x) => x.id === P.sel); if (b) { const rad = (b.rot || 0) * Math.PI / 180, off = b.h / 2 + 22 / P.CELL, cx = b.x + b.w / 2, cy = b.y + b.h / 2, kx = cx + Math.sin(rad) * off, ky = cy - Math.cos(rad) * off; if (Math.hypot(p.x - kx, p.y - ky) < R + 4 / P.CELL) { zoneDrag = { id: b.id, mode: 'rotate', cx, cy, moved: false }; cap(); return; } } }
            // zones (excl rectangles): corner handle → resize, body → move
            const dirs = ['nw', 'ne', 'se', 'sw'];
            for (let i = P.excl.length - 1; i >= 0; i--) { const b = P.excl[i], cs = quad(b);
                for (let c = 0; c < 4; c++) if (Math.hypot(cs[c][0] - p.x, cs[c][1] - p.y) < R) { P.sel = b.id; P.structSel = null; zoneDrag = { id: b.id, mode: 'resize', dir: dirs[c], o0: { x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot || 0 }, moved: false }; cap(); draw(); return; } }
            for (let i = P.excl.length - 1; i >= 0; i--) { const b = P.excl[i]; if (pointInBlock(b, p)) { P.sel = b.id; P.structSel = null; zoneDrag = { id: b.id, mode: 'move', gx: p.x - b.x, gy: p.y - b.y, moved: false }; cap(); draw(); return; } }
            // empty click → deselect only (NEVER delete); on an enclosed room, show its m²
            P.structSel = null; P.sel = null; showRoom(p); draw();
        }
        async function deleteSel() {
            if (selSet.length) { // group delete (FE4): zones removed from geometry (undoable); vehicles unpositioned via API (confirmed)
                if (!canManageNow) { clearSelSet(); draw(); return; }
                const blocks = selSet.map(findBlock).filter(Boolean);
                const vehs = blocks.filter((b) => b._id && b.kind !== 'excl'), excls = blocks.filter((b) => !(b._id && b.kind !== 'excl'));
                if (vehs.length && !(await confirmDialog('Gefährte entfernen', vehs.length + ' Gefährt(e) aus dem Plan entfernen?', 'Entfernen'))) return;
                selSet = [];
                if (excls.length) { excls.forEach((b) => { P.excl = P.excl.filter((x) => x !== b); if (P.sel === b.id) P.sel = null; }); pushUndo(); markDirty(); }
                if (vehs.length) {
                    // Count failures and report the REAL result after the API calls — don't claim
                    // every vehicle was removed when some api.del() rejected.
                    (async () => { let ok = 0, fail = 0; for (const v of vehs) { try { await api.del('/spots/' + v._id); P.spots = P.spots.filter((x) => x !== v); if (v.vehId) P.palette.push({ id: v.vehId, label: v.label, type: v.type, person_id: v.personId, person_name: v.personName, length_m: v.L, width_m: v.W, height_m: v.H, weight_t: v.t }); if (P.sel === v._id) P.sel = null; ok++; } catch (er) { fail++; } } draw(); toast(fail ? ok + ' entfernt · ' + fail + ' fehlgeschlagen' : ok + ' entfernt', fail ? 'warn' : 'ok'); })();
                } else { draw(); toast(excls.length + ' entfernt', 'ok'); }
                return;
            }
            if (!P.structSel) { if (P.sel != null) { const b = P.excl.find((x) => x.id === P.sel); if (b) removeExcl(b); } return; }
            if (P.structSel.type === 'opening') { const e = P.walls.edges[P.structSel.ei]; if (e && e.ops) e.ops.splice(P.structSel.oi, 1); P.structSel = null; pushUndo(); markDirty(); draw(); toast('Öffnung entfernt — Wand geschlossen'); return; }
            const isNode = P.structSel.type === 'node';
            if (isNode) deleteNode(P.structSel.idx); else deleteEdge(P.structSel.idx);
            P.structSel = null; refreshFloorFromWalls(); pushUndo(); markDirty(); equalizePadding(); toast(isNode ? 'Punkt aufgelöst' : 'Segment entfernt');
        }
        drawOverlay.addEventListener('pointerdown', (ev) => {
            if (ev.button !== 0 || spaceDown || P.mode !== 'plan' || P.calib || !canManageNow) return;
            ev.preventDefault(); const p = evWorld(ev);
            if (isWallDraw(P.tool)) { wallClick(snapDraw(p, ev.shiftKey)); return; }
            if (isOpenKind(P.tool)) { openClick(p); return; }
            if (isZoneTool(P.tool) || P.tool === 'column') { const x = gsnap(p.x), y = gsnap(p.y); zoneDraw = { x0: x, y0: y, x1: x, y1: y }; try { drawOverlay.setPointerCapture(ev.pointerId); } catch (er) { /* ignore */ } return; }
            editDown(ev, p);
        });
        drawOverlay.addEventListener('pointermove', (ev) => {
            let p = evWorld(ev);
            if ((P.chain || nodeDrag || zoneDraw || zoneDrag) && maybeExpand(p)) p = evWorld(ev); // auto-expand while drawing/dragging
            if (zoneDraw) { zoneDraw.x1 = gsnap(p.x); zoneDraw.y1 = gsnap(p.y); drawFloor(); return; }
            if (zoneDrag) {
                const b = P.excl.find((x) => x.id === zoneDrag.id); if (!b) { zoneDrag = null; return; }
                if (zoneDrag.mode === 'move') { b.x = Math.max(0, Math.min(P.Wm - b.w, gsnap(p.x - zoneDrag.gx))); b.y = Math.max(0, Math.min(P.Hm - b.h, gsnap(p.y - zoneDrag.gy))); snapZoneFaces(b); }
                else if (zoneDrag.mode === 'rotate') { let ang = Math.atan2(p.y - zoneDrag.cy, p.x - zoneDrag.cx) * 180 / Math.PI + 90; if (!ev.altKey) ang = Math.round(ang / 15) * 15; b.rot = ((ang % 360) + 360) % 360; }
                else { const nr = resizeBlock(zoneDrag.o0, zoneDrag.dir, p.x, p.y); b.x = Math.max(0, nr.x); b.y = Math.max(0, nr.y); b.w = nr.w; b.h = nr.h; }
                zoneDrag.moved = true; const elm = layer.querySelector('.gp-block[data-id="' + b.id + '"]'); if (elm) positionBlock(elm, b); positionWallPop(); return;
            }
            if (opDrag) {
                const e = P.walls.edges[opDrag.ei]; if (!e || !(e.ops || [])[opDrag.oi]) { opDrag = null; return; }
                const v = edgeVec(e), o = e.ops[opDrag.oi]; let t = ((p.x - v.a.x) * v.dx + (p.y - v.a.y) * v.dy) / (v.len * v.len);
                if (opDrag.mode === 'move') { const hp = (o.w / v.len) / 2; o.c = Math.max(hp, Math.min(1 - hp, t - opDrag.grab)); }
                else { t = Math.max(0, Math.min(1, t)); const hp = (o.w / v.len) / 2, fixed = opDrag.end === 'lo' ? (o.c + hp) : (o.c - hp); const lo = Math.min(t, fixed), hi = Math.max(t, fixed), wNew = (hi - lo) * v.len; if (wNew >= 0.2) { o.c = (lo + hi) / 2; o.w = round2(wNew); } }
                opDrag.moved = true; drawFloor(); positionWallPop(); return;
            }
            if (nodeDrag) {
                let x = gsnap(p.x + (nodeDrag.gx || 0)), y = gsnap(p.y + (nodeDrag.gy || 0)); const R = 12 / P.CELL, tol = 11 / P.CELL; P.guide = null; let bd = R;
                for (let i = 0; i < P.walls.nodes.length; i++) { if (i === nodeDrag.ni) continue; const n = P.walls.nodes[i]; const d = Math.hypot(n.x - x, n.y - y); if (d < bd) { bd = d; x = n.x; y = n.y; } }
                // Ortho-align to the nearest node's X (vertical) and Y (horizontal) across the WHOLE
                // plan — not just direct neighbours — so a corner clicks straight above/beside any
                // other corner. Connected neighbours are included and win when equally close.
                let bx = tol, sx = null, by = tol, sy = null;
                for (let i = 0; i < P.walls.nodes.length; i++) { if (i === nodeDrag.ni) continue; const n = P.walls.nodes[i]; const dxv = Math.abs(x - n.x); if (dxv < bx) { bx = dxv; sx = n.x; } const dyv = Math.abs(y - n.y); if (dyv < by) { by = dyv; sy = n.y; } }
                if (sx != null) { x = sx; P.guide = { kind: 'v', x }; } if (sy != null) { y = sy; P.guide = { kind: 'h', y }; }
                nodeDrag.moved = true; P.walls.nodes[nodeDrag.ni].x = round2(x); P.walls.nodes[nodeDrag.ni].y = round2(y); _encKey = null; drawFloor(); return;
            }
            if (isWallDraw(P.tool)) { const sp = snapDraw(p, ev.shiftKey); P.preview = P.chain ? sp : null; drawFloor(); return; }
            if (isOpenKind(P.tool)) {
                // live distance readout to both wall ends while hovering/placing an opening
                if (P.openStart) { const ed = P.walls.edges[P.openStart.ei]; if (ed) { const v = edgeVec(ed); let t2 = ((p.x - v.a.x) * v.dx + (p.y - v.a.y) * v.dy) / (v.len * v.len); t2 = Math.max(0, Math.min(1, t2)); P.preview = { x: v.a.x + v.dx * t2, y: v.a.y + v.dy * t2, t: t2 }; P.attach = { ei: P.openStart.ei, t: t2, x: P.preview.x, y: P.preview.y }; } }
                else { const e0 = edgeAt(p, 14 / P.CELL); P.attach = e0 ? { ei: e0.i, t: e0.t, x: e0.x, y: e0.y } : null; P.snapHint = e0 ? { x: e0.x, y: e0.y } : null; }
                drawFloor();
            }
        });
        drawOverlay.addEventListener('pointerup', (ev) => {
            if (zoneDraw) {
                const kind = P.tool, x0 = zoneDraw.x0, y0 = zoneDraw.y0;
                let x = Math.min(zoneDraw.x0, zoneDraw.x1), y = Math.min(zoneDraw.y0, zoneDraw.y1), w = Math.abs(zoneDraw.x1 - zoneDraw.x0), h = Math.abs(zoneDraw.y1 - zoneDraw.y0);
                zoneDraw = null; try { drawOverlay.releasePointerCapture(ev.pointerId); } catch (er) { /* ignore */ }
                P.tool = null; P.snapHint = null;
                // Stütze: a small structural block — a tiny drag/click places a default square.
                const isCol = kind === 'column';
                if (isCol && (w < 0.3 || h < 0.3)) { const s = (EXCL.column && EXCL.column.w) || 0.4; x = round2(x0 - s / 2); y = round2(y0 - s / 2); w = s; h = s; }
                const minOk = isCol ? (w >= 0.15 && h >= 0.15) : (w >= 0.5 && h >= 0.5);
                if (minOk) { const b = createZone(kind, x, y, w, h); P.sel = b.id; P.structSel = null; pushUndo(); markDirty(); equalizePadding(); toast((EXCL[kind] ? EXCL[kind].label : 'Fläche') + ' erstellt', 'ok'); }
                else draw();
                return;
            }
            if (zoneDrag) { const moved = zoneDrag.moved; zoneDrag = null; try { drawOverlay.releasePointerCapture(ev.pointerId); } catch (er) { /* ignore */ } if (moved) { pushUndo(); markDirty(); equalizePadding(); } else draw(); return; }
            if (opDrag) { const moved = opDrag.moved; opDrag = null; try { drawOverlay.releasePointerCapture(ev.pointerId); } catch (er) { /* ignore */ } if (moved) { pushUndo(); markDirty(); } draw(); return; }
            if (!nodeDrag) return; const moved = nodeDrag.moved; nodeDrag = null; P.guide = null; try { drawOverlay.releasePointerCapture(ev.pointerId); } catch (er) { /* ignore */ }
            if (moved) { refreshFloorFromWalls(); pushUndo(); markDirty(); equalizePadding(); } else draw();
        });
        drawOverlay.addEventListener('dblclick', (ev) => {
            if (P.mode !== 'plan' || P.calib) return; ev.preventDefault();
            if (isWallDraw(P.tool) && P.chain) { endChain(); return; }
            if (!P.tool) { const p = evWorld(ev), R = 12 / P.CELL; if (nodeJointAt(p, R) >= 0) return; const e = edgeAt(p, R); if (e) { const nn = splitEdgeAt(e.i, e.t); P.structSel = { type: 'node', idx: nn }; refreshFloorFromWalls(); pushUndo(); markDirty(); layout(); toast('Punkt eingefügt'); } }
        });
        drawOverlay.addEventListener('contextmenu', (ev) => {
            if (P.mode !== 'plan' || P.calib) return; ev.preventDefault();
            // Right-click NEVER deletes: it cancels the active drawing tool (→ selection cursor)
            // or, if nothing is being drawn, just clears the current selection.
            if (P.tool || P.chain || P.openStart || zoneDraw) { cancelDrawing(); return; }
            if (P.structSel || P.sel != null) { P.structSel = null; P.sel = null; draw(); }
        });
        drawOverlay.addEventListener('pointercancel', () => { if (nodeDrag || opDrag || zoneDrag || zoneDraw) { nodeDrag = null; opDrag = null; zoneDrag = null; zoneDraw = null; draw(); } });

        // ---- palette drag → place ----
        let drag = null, prev = null;
        rail.addEventListener('pointerdown', (e) => {
            if (e.button !== 0 || P.mode !== 'manage' || !canManageNow) return;
            const c = e.target.closest('.gp-vehc'); if (!c) return;
            const p = P.palette.find((x) => x.id == c.dataset.pid); if (!p) return; e.preventDefault();
            const g = el('div', { class: 'gp-ghost' }, '🚗 ' + p.label); document.body.append(g);
            prev = el('div', { class: 'gp-preview' }, el('span', { class: 'gp-pl' })); layer.append(prev);
            drag = { p, g }; moveDrag(e); c.setPointerCapture(e.pointerId);
        });
        rail.addEventListener('pointermove', (e) => { if (drag) moveDrag(e); });
        rail.addEventListener('pointerup', (e) => { if (drag) dropDrag(e); });
        // pointercancel (touch interruption etc.): drop any in-flight drag/edit state
        // so it can't get stuck and block further interaction.
        const cancelGesture = () => {
            if (marq) { if (marq.el) marq.el.remove(); marq = null; } // FE4: don't leave a stuck rubber-band
            if (act) { const b = act.b; if (act.mode === 'move') { b.x = act.ox; b.y = act.oy; if (act.group) act.group.forEach((m) => { m.b.x = m.ox; m.b.y = m.oy; }); } else if (act.mode === 'resize' && act.o0) { b.x = act.o0.x; b.y = act.o0.y; b.w = act.o0.w; b.h = act.o0.h; b.rot = act.o0.rot; } else if (act.mode === 'rotate') { b.rot = act.orot; } if (act.elm) act.elm.classList.remove('moving'); act = null; draw(); }
            railDrag = null; // don't leave a fullscreen rail-drag following the cursor
            if (drag) { if (drag.g) drag.g.remove(); drag = null; }
            if (prev) { prev.remove(); prev = null; }
        };
        svg.addEventListener('pointercancel', cancelGesture);
        layer.addEventListener('pointercancel', cancelGesture);
        rail.addEventListener('pointercancel', cancelGesture);
        function calcPlace(e) { const r = planEl.getBoundingClientRect(), v = drag.p, def = catFoot(v.type); const w = num(v.length_m, def[0]), h = num(v.width_m, def[1]); let x = (e.clientX - r.left) / P.CELL - w / 2, y = (e.clientY - r.top) / P.CELL - h / 2; x = gsnap(x); y = gsnap(y); const p = snapPos({ rot: 0, w, h }, x, y); return { x: p.x, y: p.y, w, h }; }
        function moveDrag(e) {
            drag.g.style.left = (e.clientX + 14) + 'px'; drag.g.style.top = (e.clientY + 14) + 'px';
            const pc = calcPlace(e), ins = rectInPoly(pc, P.floor), col = collide({ kind: 'veh', x: pc.x, y: pc.y, w: pc.w, h: pc.h }, null), bad = !ins || col;
            prev.className = 'gp-preview' + (bad ? ' bad' : ''); prev.style.left = (pc.x * P.CELL) + 'px'; prev.style.top = (pc.y * P.CELL) + 'px'; prev.style.width = (pc.w * P.CELL) + 'px'; prev.style.height = (pc.h * P.CELL) + 'px';
            prev.querySelector('.gp-pl').textContent = !ins ? 'außerhalb der Fläche' : col ? 'belegt/ausgenommen' : (pc.w.toFixed(1) + '×' + pc.h.toFixed(1) + ' m');
        }
        async function dropDrag(e) {
            const pc = calcPlace(e), rr = planEl.getBoundingClientRect(), over = e.clientX >= rr.left && e.clientX <= rr.right && e.clientY >= rr.top && e.clientY <= rr.bottom;
            drag.g.remove(); if (prev) { prev.remove(); prev = null; } const p = drag.p; drag = null;
            if (!over) { toast('Auf die Fläche ziehen', 'warn'); return; }
            if (!rectInPoly(pc, P.floor)) { toast('Außerhalb der Hallenfläche', 'error'); return; }
            if (collide({ kind: 'veh', x: pc.x, y: pc.y, w: pc.w, h: pc.h }, null)) { toast('Kein Platz frei — belegt/ausgenommen', 'error'); return; }
            try {
                const geom = { x: round2(pc.x), y: round2(pc.y), w: round2(pc.w), h: round2(pc.h), rot: 0, status: 'busy' };
                // Hall-unique persisted spot label (uq_spots_hall_label) — suffix the vehicle id
                // so two like-named Gefährte don't collide; p.label stays the display label.
                const spotLabel = (p.label + ' #' + p.id).slice(0, 90);
                const spot = await api.post('/halls/' + P.hallId + '/spots', { label: spotLabel, geometry: geom });
                // Two-step: the placement footprint then its 1:1 vehicle link. If the
                // link fails, roll the just-created spot back so no orphan lingers.
                try { await api.put('/spots/' + spot.id + '/vehicle', { vehicle_id: p.id }); }
                catch (linkErr) { try { await api.del('/spots/' + spot.id); } catch (e2) { /* best effort */ } throw linkErr; }
                P.spots.push({ _id: spot.id, kind: 'veh', label: p.label, spotLabel: spot.label || spotLabel, type: p.type || '', vehId: p.id, personId: p.person_id || null, personName: p.person_name || '', L: p.length_m, W: p.width_m, H: p.height_m, t: p.weight_t, x: pc.x, y: pc.y, w: pc.w, h: pc.h, rot: 0, status: 'busy', _dirty: false });
                P.palette = P.palette.filter((x) => x.id !== p.id); P.sel = spot.id;
                const nb = P.spots[P.spots.length - 1];
                if ((nb.H != null && nb.H > P.tor) || (nb.t != null && nb.t > P.load)) toast('⚠ ' + p.label + ' platziert · Höhe/Gewicht', 'warn'); else toast('✓ ' + p.label + ' platziert', 'ok');
                draw();
            } catch (err) { toast(err.message || 'Platzieren fehlgeschlagen', 'error'); }
        }

        // Arrive-from-vehicle focus: select the target spot, then scroll it into view
        // and pulse it once so it's easy to spot. Consumed once.
        function focusSpot(id) {
            const elm = layer.querySelector('.gp-block[data-id="' + id + '"]'); if (!elm) return;
            try { elm.scrollIntoView({ block: 'center', inline: 'center', behavior: 'smooth' }); } catch (e) { elm.scrollIntoView(); }
            elm.classList.add('gp-focus'); setTimeout(() => elm.classList.remove('gp-focus'), 2600);
        }
        const focusTarget = (pendingSpotFocus != null && P.spots.some((s) => s._id === pendingSpotFocus)) ? pendingSpotFocus : null;
        pendingSpotFocus = null;
        if (focusTarget != null) P.sel = focusTarget;

        // initial paint — sync the floor bound to any loaded walls first
        if (P.walls.edges.length) refreshFloorFromWalls();
        setMode(P.mode);
        // Fit FIRST, then take the undo baseline — fitView() canonicalises coordinates (shiftWorld),
        // so recording the baseline afterwards keeps hist[0] == what's on screen (no undo jump) (F4).
        requestAnimationFrame(() => { if (P.walls.edges.length) fitView(); else layout(); pushUndo(); if (focusTarget != null) requestAnimationFrame(() => focusSpot(focusTarget)); });
    }

    function openMenu() {
        const dlg = $('#menu');
        const body = $('#menu-body');
        body.innerHTML = '';
        body.append(el('div', { class: 'sheet-user' },
            el('div', { class: 'name' }, esc(state.user.username)),
            el('div', { class: 'muted' }, ROLE_LABEL[state.user.role] || state.user.role)));
        const item = (ic, label, fn, cls = '') => {
            const b = el('button', { class: 'menu-item ' + cls }, el('span', { class: 'ic' }, icon(ic, 18)), el('span', {}, label));
            b.addEventListener('click', () => { dlg.close(); fn(); });
            return b;
        };
        body.append(item('box', 'Stellplätze', () => navigate('garages')));
        body.append(item('settings', 'Einstellungen', () => navigate('settings')));
        if (isAdmin()) body.append(item('users', 'Benutzer', () => navigate('users')));
        if (isAdmin()) body.append(item('log', 'Audit-Log', () => navigate('audit')));
        if (isAdmin()) body.append(item('receipt', 'Rechnungen', () => navigate('billing')));
        if (isAdmin()) body.append(item('archive', 'Backup', () => navigate('backup')));
        body.append(item('theme', 'Design wechseln', () => toggleTheme()));
        body.append(item('logout', 'Abmelden', () => logout(), 'danger'));
        dlg.showModal();
    }

    // ---------- auth / bootstrap ----------
    async function showApp() {
        $('#login-view').hidden = true;
        $('#app-view').hidden = false;
        if (!location.hash || location.hash === '#') location.hash = '#/dashboard';
        else render();
    }
    const EYE_SVG = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>';
    const EYE_OFF_SVG = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 12s3.6-7 10-7c1.7 0 3.2.5 4.5 1.2M22 12s-3.6 7-10 7c-1.7 0-3.2-.5-4.5-1.2"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/><path d="M3 3l18 18"/></svg>';
    // A show/hide eye button bound to a password input, for reuse across the
    // login card and any modal password field.
    function pwToggleBtn(input) {
        const btn = el('button', { type: 'button', class: 'affix-btn', 'aria-label': 'Passwort anzeigen', 'aria-pressed': 'false' });
        btn.innerHTML = EYE_SVG;
        btn.addEventListener('click', () => {
            const vis = input.type === 'password';
            input.type = vis ? 'text' : 'password';
            btn.setAttribute('aria-pressed', String(vis));
            btn.setAttribute('aria-label', vis ? 'Passwort verbergen' : 'Passwort anzeigen');
            btn.innerHTML = vis ? EYE_OFF_SVG : EYE_SVG;
        });
        return btn;
    }
    // Toggle password field visibility (login show/hide affix button).
    function setPwVisible(vis) {
        const inp = $('#login-password'); const btn = $('#login-pw-toggle');
        if (!inp || !btn) return;
        inp.type = vis ? 'text' : 'password';
        btn.setAttribute('aria-pressed', String(vis));
        btn.setAttribute('aria-label', vis ? 'Passwort verbergen' : 'Passwort anzeigen');
        btn.innerHTML = vis ? EYE_OFF_SVG : EYE_SVG;
    }
    // 2FA entry: the app code uses six single-digit slots (clearer, auto-advancing,
    // paste-aware); the backup code a single grouped field. Both feed the same login
    // submit via getTotpValue(), so the auth path is unchanged — the backend accepts
    // either.
    let totpBackupMode = false;
    function buildOtp() {
        const g = $('#login-otp');
        if (!g || g.children.length) return; // build once
        for (let i = 0; i < 6; i++) {
            g.append(el('input', {
                class: 'otp-slot', id: 'login-otp-' + i, type: 'text', inputmode: 'numeric',
                maxlength: 1, 'aria-label': `Ziffer ${i + 1} von 6`,
                autocomplete: i === 0 ? 'one-time-code' : 'off',
            }));
        }
        const slots = $$('.otp-slot', g);
        slots.forEach((s, i) => {
            s.addEventListener('input', () => {
                const digits = s.value.replace(/\D/g, '');
                if (digits.length > 1) {
                    // Multi-digit input (one-time-code autofill / IME) lands in one
                    // slot: spread it across this and the following slots instead of
                    // dropping all but the first digit.
                    [...digits].forEach((c, k) => {
                        const t = slots[i + k];
                        if (!t) return;
                        t.value = c;
                        t.classList.add('is-filled');
                    });
                    (slots.slice(i).find((t) => !t.value) || slots[slots.length - 1]).focus();
                    return;
                }
                s.value = digits;
                s.classList.toggle('is-filled', !!digits);
                if (digits && i < 5) slots[i + 1].focus();
            });
            s.addEventListener('keydown', (e) => {
                if (e.key === 'Backspace' && !s.value && i > 0) { e.preventDefault(); slots[i - 1].focus(); }
            });
            s.addEventListener('paste', (e) => {
                e.preventDefault();
                const d = (e.clipboardData.getData('text') || '').replace(/\D/g, '').slice(0, 6);
                if (!d) return;
                [...d].forEach((c, k) => { if (slots[k]) { slots[k].value = c; slots[k].classList.add('is-filled'); } });
                (slots[Math.min(d.length, 5)] || slots[5]).focus();
            });
        });
    }
    const otpValue = () => $$('.otp-slot').map((s) => s.value).join('');
    const getTotpValue = () => (totpBackupMode ? $('#login-backup').value.trim() : otpValue());
    function clearTotp() {
        const bk = $('#login-backup'); if (bk) bk.value = '';
        $$('.otp-slot').forEach((s) => { s.value = ''; s.classList.remove('is-filled'); });
    }
    function focusTotp() {
        if (totpBackupMode) { const bk = $('#login-backup'); if (bk) bk.focus(); }
        else { const s = $('.otp-slot'); if (s) s.focus(); }
    }
    // Switch between the app-code slots and the single backup-code field.
    function setTotpMode(backup) {
        totpBackupMode = !!backup;
        const otp = $('#login-otp'), bk = $('#login-backup');
        if (!otp || !bk) return;
        otp.hidden = backup;
        bk.hidden = !backup;
        const label = $('#login-totp-label');
        label.setAttribute('for', backup ? 'login-backup' : 'login-otp-0');
        label.textContent = backup ? 'Backup-Code' : 'Zwei-Faktor-Code';
        $('#login-backup-toggle').textContent = backup ? 'Stattdessen App-Code' : 'Backup-Code verwenden';
        $('#login-totp-help').textContent = backup
            ? 'Einmal-Code aus deiner Backup-Liste (Format ABCD-EFGH-IJKL).'
            : '6-stelliger Code aus der App – oder ein Backup-Code, falls kein Handy zur Hand.';
        clearTotp();
    }
    function showLogin() {
        $('#app-view').hidden = true;
        $('#login-view').hidden = false;
        $('#login-totp-wrap').hidden = true;
        $('#login-form').classList.remove('is-2fa');
        setTotpMode(false);
        setPwVisible(false);
        const pkBtn = $('#passkey-login');
        const showPk = !!(state.capabilities.passkeys && webauthnSupported());
        if (pkBtn) pkBtn.hidden = !showPk;
        const or = $('#login-or'); if (or) or.hidden = !showPk;
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
            const opts = await withStepUp((pw) => api.post('/passkeys/register/begin', { password: pw }));
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
            if (e && (e.name === 'NotAllowedError' || e.cancelled)) return; // user cancelled the prompt / step-up
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
            const totp = getTotpValue();
            if (totp) body.totp_code = totp;
            try {
                state.user = await api.post('/auth/login', body);
                $('#login-password').value = ''; clearTotp();
                showApp();
            } catch (err) {
                if (err.data && err.data.totp_required) {
                    $('#login-totp-wrap').hidden = false;
                    $('#login-form').classList.add('is-2fa');
                    focusTotp();
                    errEl.textContent = totp ? err.message : 'Bitte Zwei-Faktor-Code eingeben.';
                } else {
                    // A plain auth failure (not a 2FA prompt) — e.g. the user went
                    // back and mistyped the password — so clear any stale 2FA step
                    // rather than leaving a "Passwort korrekt" banner next to an
                    // "invalid credentials" error.
                    $('#login-totp-wrap').hidden = true;
                    $('#login-form').classList.remove('is-2fa');
                    setTotpMode(false); // resets to app-code mode and clears the slots
                    errEl.textContent = err.message;
                }
                errEl.hidden = false;
            }
        });
        const pkBtn = $('#passkey-login');
        if (pkBtn) pkBtn.addEventListener('click', passkeyLogin);
        buildOtp();
        $('#login-pw-toggle').addEventListener('click', () => setPwVisible($('#login-password').type === 'password'));
        $('#login-backup-toggle').addEventListener('click', () => setTotpMode(!totpBackupMode));
        $('#theme-btn').addEventListener('click', toggleTheme);
        $('#menu-btn').addEventListener('click', openMenu);
        $$('.tab').forEach((t) => t.addEventListener('click', () => navigate(t.dataset.route)));
        for (const id of ['#modal', '#confirm', '#menu']) {
            $(id).addEventListener('click', (e) => {
                if (e.target !== e.currentTarget) return;
                const guard = e.currentTarget._canClose; // a gated contentModal blocks backdrop dismissal
                if (!guard || guard()) e.currentTarget.close();
            });
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

    // ---------- rotating brand vehicle (login logo / topbar logo / favicon) ----------
    // Picks one vehicle per session (persisted), so refreshes stay stable and a new
    // session shows a new one — the login logo, topbar logo and favicon all match.
    const VEHICLES = [
        '<path d="M4.5 12 12 5.2 19.5 12"/><path d="M6.6 12.2v6.3M17.4 12.2v6.3"/><rect x="9" y="14.4" width="6" height="4" rx="1.1"/>',
        '<path d="M4.5 15.5v-3c0-.5.15-1 .45-1.4l1.7-2.3c.35-.5.9-.8 1.5-.8h6.7c.6 0 1.15.3 1.5.75l1.75 2.35c.3.4.45.9.45 1.4v3"/><path d="M2.5 15.5h19"/><circle cx="7.3" cy="16.3" r="1.7"/><circle cx="16.7" cy="16.3" r="1.7"/>',
        '<path d="M3 15V11a3 3 0 0 1 3-3h9.7a2.8 2.8 0 0 1 2.8 2.8V15Z"/><path d="M2.5 15h16.5"/><path d="M18.5 13.2H22"/><rect x="5.5" y="10" width="4.5" height="3.3" rx=".6"/><circle cx="12.6" cy="16.3" r="1.8"/>',
        '<path d="M3 14.5V11h13.5v3.5"/><path d="M3 14.5h13.5"/><path d="M16.5 12.8H21"/><circle cx="9" cy="16.3" r="1.8"/>',
        '<path d="M3.5 13.5h16.5l-1.7 3.3a2 2 0 0 1-1.8 1.1H7a2 2 0 0 1-1.8-1.1L3.5 13.5Z"/><path d="M8.5 13.5v-3l4 1.3v1.7"/><path d="M2.5 20.4c1.4.9 2.7.9 4 0s2.6-.9 4 0 2.7.9 4 0 2.6-.9 4 0"/>',
        '<path d="M2.5 16V9.2A1.2 1.2 0 0 1 3.7 8H13v8"/><path d="M13 8h3.4c.4 0 .8.2 1.05.5l2.3 3c.25.35.4.75.4 1.2V16H13"/><rect x="4.5" y="10" width="4" height="3" rx=".5"/><circle cx="7" cy="16.6" r="1.7"/><circle cx="16.6" cy="16.6" r="1.7"/>',
        '<path d="M2.5 16v-4.8C2.5 9 4 7.5 6.2 7.5h9.6C18 7.5 19.5 9 19.5 11.2V16"/><path d="M2.5 12h17M11 8v4"/><circle cx="6.5" cy="16.6" r="1.7"/><circle cx="15.5" cy="16.6" r="1.7"/>',
        '<circle cx="5.5" cy="15.5" r="3.2"/><circle cx="18.5" cy="15.5" r="3.2"/><path d="M5.5 15.5l3.2-4.5h4.3l1.9 3.1"/><path d="M8.7 11 7.9 8.4H11"/><path d="M15 14.2l3.5 1.3"/>',
        '<path d="M4 16.4h14l-1.6 2.6a1.8 1.8 0 0 1-1.5.9H7.1a1.8 1.8 0 0 1-1.5-.9L4 16.4Z"/><path d="M11.2 15V3.6L17.6 15Z"/><path d="M11.2 15H5.4L11.2 8.6"/>',
        '<path d="M3 14.2h13.6a3 3 0 0 1-3 2.9H6a3 3 0 0 1-3-2.9Z"/><path d="M11 14l3.3-3.2 1.8 1"/><path d="M2.5 20c1.3.9 2.6.9 4 0s2.6-.9 4 0 2.6.9 4 0 2.6-.9 4 0"/>',
        '<path d="M3 15.5v-2.2l1.6-.6 1.4-3c.3-.6.9-1 1.6-1h6c.6 0 1.2.3 1.5.9l1.4 2.9 2.5.9v2.1"/><path d="M2.5 15.5h19"/><path d="M6 12.7h5"/><circle cx="7.3" cy="16.3" r="1.7"/><circle cx="16.7" cy="16.3" r="1.7"/>',
    ];
    // Pick once per browser session and remember it: a normal refresh (F5 or a
    // cache-bypassing hard reload — indistinguishable to a page) keeps the same
    // vehicle, while a new session or cleared site data rolls a fresh one.
    function pickVehicleIndex() {
        let idx = -1;
        try { idx = parseInt(sessionStorage.getItem('parkrr-veh'), 10); } catch { /* storage blocked */ }
        if (!(idx >= 0 && idx < VEHICLES.length)) {
            idx = Math.floor(Math.random() * VEHICLES.length);
            try { sessionStorage.setItem('parkrr-veh', String(idx)); } catch { /* storage blocked */ }
        }
        return idx;
    }
    function applyBrand() {
        const inner = VEHICLES[pickVehicleIndex()];
        const mk = (size) => `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${inner}</svg>`;
        $$('.brand-veh').forEach((node) => { node.innerHTML = mk(node.dataset.veh || 24); });
        // Favicon: a filled teal tile + white glyph so it reads at tab size, and it
        // rotates with the logos. Drop every existing icon link first, otherwise the
        // static favicon.ico (sizes="any") wins and the dynamic one is ignored.
        const raw = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#0d9488"/><g fill="none" stroke="#ffffff" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">${inner}</g></svg>`;
        document.querySelectorAll('link[rel~="icon"]').forEach((l) => l.remove());
        const link = document.createElement('link');
        link.rel = 'icon';
        link.type = 'image/svg+xml';
        link.href = 'data:image/svg+xml,' + encodeURIComponent(raw);
        document.head.appendChild(link);
    }

    // Global command palette (⌘K / search button): fuzzy jump to a person or
    // Gefährt. Works over the loaded lists (small dataset) — no backend needed.
    async function openCommandPalette() {
        if (!state.user || document.getElementById('cmdk')) return;
        const overlay = el('div', { id: 'cmdk', class: 'cmdk' });
        const input = el('input', { class: 'cmdk-input', type: 'search', placeholder: 'Person, Gefährt, Kennzeichen …', autocomplete: 'off', 'aria-label': 'Suche' });
        const results = el('div', { class: 'cmdk-results' });
        overlay.append(el('div', { class: 'cmdk-box' }, input, results));
        document.body.append(overlay);
        input.focus();

        let items = [], sel = 0;
        const close = () => { overlay.remove(); document.removeEventListener('keydown', onKey, true); };
        overlay.addEventListener('mousedown', (e) => { if (e.target === overlay) close(); });

        let persons = state.persons || [], vehicles = [];
        try { [persons, vehicles] = await Promise.all([persons.length ? Promise.resolve(persons) : api.get('/persons'), api.get('/vehicles')]); }
        catch (e) { /* search what we already have */ }

        const build = () => {
            const q = norm(input.value.trim());
            items = [];
            if (q) {
                persons.forEach((p) => { if (norm(personName(p) + ' ' + (p.email || '') + ' ' + (p.phone || '')).includes(q)) items.push({ type: 'Person', label: personName(p), sub: [p.email, p.phone].filter(Boolean).join(' · '), nav: 'persons/' + p.id }); });
                vehicles.forEach((v) => { if (norm(vehicleTitle(v) + ' ' + (v.license_plate || '') + ' ' + (v.person_name || '') + ' ' + (v.category_name || '')).includes(q)) items.push({ type: 'Gefährt', label: vehicleTitle(v), sub: [v.license_plate, v.person_name].filter(Boolean).join(' · '), nav: 'vehicles/' + v.id }); });
            }
            items = items.slice(0, 12);
            if (sel >= items.length) sel = Math.max(0, items.length - 1);
            results.innerHTML = '';
            if (!q) { results.append(el('div', { class: 'cmdk-hint' }, '↑↓ wählen · Enter öffnen · Esc schließen')); return; }
            if (!items.length) { results.append(el('div', { class: 'cmdk-hint' }, 'Keine Treffer.')); return; }
            items.forEach((it, i) => results.append(el('div', { class: 'cmdk-row' + (i === sel ? ' on' : ''), onclick: () => { close(); navigate(it.nav); } },
                el('span', { class: 'cmdk-type' }, it.type),
                el('span', { class: 'cmdk-lbl' }, it.label),
                it.sub ? el('span', { class: 'cmdk-sub' }, it.sub) : null)));
        };
        const onKey = (e) => {
            if (e.key === 'Escape') { e.preventDefault(); close(); }
            else if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(items.length - 1, sel + 1); build(); }
            else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(0, sel - 1); build(); }
            else if (e.key === 'Enter') { e.preventDefault(); if (items[sel]) { close(); navigate(items[sel].nav); } }
        };
        document.addEventListener('keydown', onKey, true);
        input.addEventListener('input', () => { sel = 0; build(); });
        build();
    }

    // Public self-service portal (no login): rendered when the hash is
    // #/portal/<token>. Read-only view of one person's vehicles + invoices.
    async function renderPortal(token) {
        const lv = $('#login-view'); if (lv) lv.hidden = true;
        const av = $('#app-view'); if (av) av.hidden = true;
        const pv = $('#portal-view');
        pv.hidden = false;
        pv.innerHTML = '';
        pv.append(skeleton(4));
        let sum;
        try { sum = await api.get('/portal/' + token + '/summary'); }
        catch (e) {
            pv.innerHTML = '';
            pv.append(el('div', { class: 'portal-wrap' }, el('div', { class: 'portal-card' },
                el('h1', {}, 'Parkrr'),
                el('p', { class: 'muted' }, 'Dieser Link ist ungültig oder abgelaufen. Bitte fordern Sie einen neuen an.'))));
            return;
        }
        pv.innerHTML = '';
        const wrap = el('div', { class: 'portal-wrap' });
        wrap.append(el('div', { class: 'portal-head' },
            el('span', { class: 'brand-veh', 'data-veh': '34', 'aria-hidden': 'true' }),
            el('div', {}, el('h1', {}, 'Parkrr'), el('p', { class: 'muted' }, esc(sum.person_name)))));
        wrap.append(el('div', { class: 'portal-card' },
            el('div', { class: 'muted' }, 'Offener Betrag'),
            el('div', { class: 'portal-amt' + (sum.open_total > 0.005 ? ' owe' : '') }, eur(sum.open_total))));
        const vcard = el('div', { class: 'portal-card' }, el('h2', {}, 'Ihre Gefährte'));
        if (!sum.vehicles.length) vcard.append(el('p', { class: 'muted' }, 'Keine aktiven Gefährte.'));
        else sum.vehicles.forEach((v) => vcard.append(el('div', { class: 'portal-row' },
            el('span', {}, esc(v.label)), el('span', { class: 'badge' }, STATUS_LABEL[v.status] || v.status))));
        wrap.append(vcard);
        const icard = el('div', { class: 'portal-card' }, el('h2', {}, 'Rechnungen'));
        if (!sum.invoices.length) icard.append(el('p', { class: 'muted' }, 'Keine Rechnungen.'));
        else sum.invoices.forEach((iv) => {
            icard.append(
                el('a', { class: 'portal-row link', href: '/api/portal/' + token + '/invoices/' + iv.id + '/pdf', target: '_blank', rel: 'noopener' },
                    el('span', {}, 'Rechnung ' + esc(iv.number),
                        el('span', { class: 'muted', style: 'display:block;font-size:.78rem' }, new Date(iv.issued_on).toLocaleDateString('de-DE') + (iv.status ? ' · ' + esc(iv.status) : ''))),
                    el('span', { style: 'text-align:right' }, eur(iv.total),
                        iv.open > 0.005 ? el('span', { class: 'muted', style: 'display:block;font-size:.78rem' }, 'offen ' + eur(iv.open)) : null)));
            // Scan-to-pay QR for each still-open invoice.
            if (iv.open > 0.005) {
                icard.append(el('div', { style: 'text-align:center;padding:.4rem 0 .2rem' },
                    el('img', { src: '/api/portal/' + token + '/invoices/' + iv.id + '/pay-qr', alt: 'SEPA-Zahlungs-QR', width: 150, height: 150, style: 'max-width:150px;height:auto' }),
                    el('div', { class: 'muted', style: 'font-size:.72rem' }, 'Scan zum Bezahlen (SEPA)')));
            }
        });
        wrap.append(icard);
        wrap.append(el('p', { class: 'portal-foot muted' }, 'Read-only Ansicht · Parkrr'));
        pv.append(wrap);
    }

    async function init() {
        initTheme();
        applyBrand();
        // Public portal short-circuits the whole app shell / auth flow.
        const pm = (location.hash || '').match(/^#\/portal\/([A-Za-z0-9_-]+)$/);
        if (pm) { await renderPortal(pm[1]); return; }
        bindStatic();
        setupInstallPrompt();
        setupOfflineIndicator();
        try { state.capabilities = await api.get('/auth/capabilities'); } catch { state.capabilities = {}; }
        try { state.user = await api.get('/auth/me'); showApp(); }
        catch { showLogin(); }
        if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
        const sb = $('#search-btn'); if (sb) sb.addEventListener('click', () => openCommandPalette());
        document.addEventListener('keydown', (e) => {
            if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K') && state.user) { e.preventDefault(); openCommandPalette(); }
        });
    }
    document.addEventListener('DOMContentLoaded', init);
})();
