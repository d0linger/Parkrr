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
        const btn = el('button', { class: 'toast-undo', type: 'button' }, actionLabel);
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
            const cleanup = () => {
                form.removeEventListener('submit', onSubmit);
                $('#modal-cancel').removeEventListener('click', onCancel);
                $('#modal-close').removeEventListener('click', onCancel);
            };
            const onCancel = () => { cleanup(); dlg.close(); resolve(null); };
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
                if (!save) { cleanup(); dlg.close(); resolve(data); return; }
                // Async save: keep the modal open, show progress, surface errors inline.
                formErr.hidden = true;
                setPending(true);
                try {
                    await save(data);
                    cleanup(); dlg.close(); resolve(data);
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
        mountList(page, {
            title: 'Personen', emptyIcon: 'users', emptyText: 'Noch keine Personen.',
            onAdd: canManage() ? () => personForm() : null,
            items: state.persons,
            searchText: (p) => norm(personName(p) + ' ' + p.email + ' ' + p.phone),
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
                            known ? el('div', { class: 'pcard__status' }, owes
                                ? el('span', { class: 'pcard__owe', title: 'Offener Saldo' }, eur(bal))
                                : el('span', { class: 'pcard__paid', title: 'Keine offenen Beträge' }, 'Bezahlt')) : null,
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
            el('h2', { style: 'margin:0' }, stats.person_name || 'Person')));

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
            el('button', { class: 'btn btn-ghost btn-sm', onclick: () => window.print() }, icon('receipt', 15), ' Drucken / PDF')));
        if (iv.status === 'teilbezahlt' || iv.status === 'bezahlt') {
            page.append(el('div', { class: 'card-meta no-print', style: 'margin:-.3rem 0 .4rem' }, 'Bezahlt ' + eur(iv.paid_amount) + ' von ' + eur(iv.total) + (iv.open_amount > 0.005 ? ' · offen ' + eur(iv.open_amount) : '')));
        }
        page.append(invoiceDocument(iv));
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
        const [vehicles, photos, history] = await Promise.all([
            api.get('/vehicles'), api.get('/vehicles/' + id + '/photos'), api.get('/vehicles/' + id + '/history'),
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
            el('h2', { style: 'margin:0;flex:1' }, esc(vehicleTitle(v))), statusBadge(v.status)));

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

    const EXCL = { lane: { label: 'Fahrstraße', w: 6, h: 2 }, maint: { label: 'Wartung', w: 4, h: 3 }, wall: { label: 'Säule/Wand', w: 1, h: 1 }, exit: { label: 'Notausgang', w: 2, h: 1 }, gate: { label: 'Tor', w: 3, h: 0.5 } };
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
            excl: (Array.isArray(geo.excl) ? geo.excl : []).map((e, i) => ({ id: 'x' + i, kind: e.kind || 'wall', x: num(e.x, 0), y: num(e.y, 0), w: num(e.w, 1), h: num(e.h, 1), rot: num(e.rot, 0), label: e.label || (EXCL[e.kind] ? EXCL[e.kind].label : 'Fläche') })),
            spots: (data.spots || []).map((s) => { const g = asObj(s.geometry); return {
                _id: s.id, kind: 'veh', label: s.vehicle_label || s.label, spotLabel: s.label, type: s.vehicle_type || '', vehId: s.vehicle_id || null,
                personId: s.person_id || null, personName: s.person_name || '',
                L: s.length_m, W: s.width_m, H: s.height_m, t: s.weight_t, needsPower: !!s.needs_power,
                plannerSymbol: s.planner_symbol || null, photoUrl: s.photo_id ? '/api/photos/' + s.photo_id : null,
                x: num(g.x, 1), y: num(g.y, 1), w: num(g.w, s.length_m || catFoot(s.vehicle_type)[0]), h: num(g.h, s.width_m || catFoot(s.vehicle_type)[1]), rot: num(g.rot, 0), status: g.status || 'busy', _dirty: false }; }),
            palette, plannerIcons,
            mode: 'manage', editMode: false, snap: true, gridStep: 0.5, ortho: false, zoom: 1, sel: null, selEdge: null,
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
        const blocksAll = () => P.spots.concat(P.excl.map((e) => ({ ...e, kind: 'excl' })));
        const collide = (cand, selfId) => { const cq = quad(cand); for (const o of blocksAll()) { if (o._id === selfId || o.id === selfId) continue; if (!(cand.kind === 'veh' || o.kind === 'veh')) continue; if (satOverlap(cq, quad(o))) return true; } return false; };
        const validVeh = (cand, selfId) => inside(cand) && !collide(cand, selfId);
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

        // edge-length editor (Garagenplaner)
        const lenIn = el('input', { type: 'number', step: '0.01', min: '0.1', class: 'gp-lenin' });
        const lenBox = el('div', { class: 'gp-lenbox' }, lenIn, el('span', { class: 'muted' }, 'm'));
        lenBox.style.display = 'none'; planEl.append(lenBox);
        // Vertex context menu (shown on click, not permanently) — declutters the plan.
        const vertMenu = el('div', { class: 'gp-vertmenu' }); vertMenu.style.display = 'none'; planEl.append(vertMenu);
        function hideVertMenu() { vertMenu.style.display = 'none'; }
        function showVertMenu(vi) {
            vertMenu.innerHTML = '';
            vertMenu.append(el('div', { class: 'gp-vm-title' }, 'Ecke ' + (vi + 1)));
            if (P.floor.length > 3) { const del = el('button', { class: 'gp-vm-btn danger' }, '✕ Punkt löschen'); del.addEventListener('click', () => { P.floor.splice(vi, 1); hideVertMenu(); commitGeom('Punkt entfernt'); }); vertMenu.append(del); }
            else vertMenu.append(el('div', { class: 'gp-vm-note' }, 'Mindestens 3 Ecken'));
            let lx = P.floor[vi][0] * P.CELL, ly = P.floor[vi][1] * P.CELL - 12;
            lx = Math.max(70, Math.min(P.Wm * P.CELL - 70, lx)); ly = Math.max(34, Math.min(P.Hm * P.CELL - 10, ly));
            vertMenu.style.left = lx + 'px'; vertMenu.style.top = ly + 'px'; vertMenu.style.display = 'flex';
        }
        lenIn.addEventListener('pointerdown', (e) => e.stopPropagation());
        lenIn.addEventListener('keydown', (e) => { e.stopPropagation(); if (e.key === 'Enter') lenIn.blur(); });
        lenIn.addEventListener('change', () => { if (P.selEdge == null) return; const L = parseFloat(lenIn.value); if (L > 0.05) { setEdgeLen(P.selEdge, L); } });

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
        function edgeLen(i) { const n = P.floor.length, a = P.floor[i], b = P.floor[(i + 1) % n]; return Math.hypot(b[0] - a[0], b[1] - a[1]); }
        function setEdgeLen(i, L) { const n = P.floor.length, a = P.floor[i], b = P.floor[(i + 1) % n], d = Math.hypot(b[0] - a[0], b[1] - a[1]); if (d < 0.01 || !(L > 0)) return; const nb = [a[0] + (b[0] - a[0]) / d * L, a[1] + (b[1] - a[1]) / d * L]; nb[0] = Math.max(-100, Math.min(200, nb[0])); nb[1] = Math.max(-100, Math.min(200, nb[1])); P.floor[(i + 1) % n] = nb; refit(); pushUndo(); markDirty(); layout(); toast('Länge ' + L.toFixed(2) + ' m'); }
        // After a free floor edit, re-home the polygon into [0,Wm]×[0,Hm]: shift any
        // negative coords to origin and grow Wm/Hm to fit, so enlarging an edge works.
        function refit() { let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity; P.floor.forEach(([x, y]) => { minX = Math.min(minX, x); minY = Math.min(minY, y); maxX = Math.max(maxX, x); maxY = Math.max(maxY, y); }); const dx = minX < 0 ? -minX : 0, dy = minY < 0 ? -minY : 0; if (dx || dy) { P.floor = P.floor.map(([x, y]) => [x + dx, y + dy]); P.excl.forEach((e) => { e.x += dx; e.y += dy; }); P.spots.forEach((s) => { s.x += dx; s.y += dy; s._dirty = true; }); maxX += dx; maxY += dy; } P.Wm = Math.max(6, Math.ceil(maxX - 1e-6)); P.Hm = Math.max(5, Math.ceil(maxY - 1e-6)); clampAll(); }

        // ---- undo/redo (index-based history over hall geometry + placement
        // geometry, not existence). The current state always lives at hist[hpos];
        // pushUndo() records the state AFTER a mutation, undo/redo step the pointer. ----
        const snapshot = () => JSON.stringify({ floor: P.floor, Wm: P.Wm, Hm: P.Hm, tor: P.tor, load: P.load, shape: P.shape, excl: P.excl, spots: P.spots.map((s) => ({ _id: s._id, x: s.x, y: s.y, w: s.w, h: s.h, rot: s.rot, status: s.status })) });
        // restore reverts local state; a spot is flagged _dirty ONLY when a value
        // actually changed, so undo/redo can re-persist exactly those to the server.
        function restore(js) {
            const st = JSON.parse(js); P.floor = st.floor; P.Wm = st.Wm; P.Hm = st.Hm; P.tor = st.tor; P.load = st.load; P.shape = st.shape; P.excl = st.excl;
            st.spots.forEach((g) => { const s = P.spots.find((x) => x._id === g._id); if (!s) return;
                if (s.x !== g.x || s.y !== g.y || s.w !== g.w || s.h !== g.h || s.rot !== g.rot || s.status !== g.status) { s.x = g.x; s.y = g.y; s.w = g.w; s.h = g.h; s.rot = g.rot; s.status = g.status; s._dirty = true; } });
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
            else if (e.key === 'Escape' && P.maxed) { toggleMax(false); }
            else if (!typing && !m && e.key.toLowerCase() === 'f') { e.preventDefault(); P.zoom = 1; layout(); toast('Eingepasst'); } // F = Einpassen
            else if (!typing && !m && e.key === ' ') { e.preventDefault(); if (!spaceDown) { spaceDown = true; planWrap.style.cursor = 'grab'; } } // Space = Pan-Modus
        };
        const keyUpHandler = (e) => { if (!root.isConnected) { window.removeEventListener('keyup', keyUpHandler); return; } if (e.key === ' ') { spaceDown = false; if (!midPan) planWrap.style.cursor = ''; } };
        window.addEventListener('keydown', keyHandler);
        window.addEventListener('keyup', keyUpHandler);

        function hideLen() { P.selEdge = null; lenBox.style.display = 'none'; }

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
            P.CELL = Math.max(6, Math.round(fit * (P.zoom || 1)));
            const pw = P.Wm * P.CELL, ph = P.Hm * P.CELL;
            planEl.style.width = pw + 'px'; planEl.style.height = ph + 'px';
            svg.setAttribute('width', pw); svg.setAttribute('height', ph); svg.setAttribute('viewBox', '0 0 ' + pw + ' ' + ph);
            // Fullscreen: fill the budget (big canvas). Normal: wrap the plan tightly
            // (+margins) up to the budget, so a short hall isn't marooned in a tall box.
            const boxH = P.maxed ? maxBudget : Math.min(maxBudget, ph + 2 * M + 2); // +2 = the wrapper's 1px borders (border-box), else a 2px scrollbar shows
            planWrap.style.height = boxH + 'px';
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
            const nz = Math.max(0.5, Math.min(4, +(P.zoom * factor).toFixed(3)));
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
            const used = P.spots.length;
            occN.textContent = used + ' Gefährte';
            const tot = polyArea(P.floor); let ve = 0; P.spots.forEach((b) => { ve += b.w * b.h; });
            occBar.style.width = Math.round(Math.min(100, ve / Math.max(1, tot) * 100)) + '%';
        }
        function drawFloor() {
            svg.innerHTML = '';
            // While editing the floor the SVG must sit ABOVE the block layer so its
            // vertex/edge/length handles receive pointer events (layer covers the plan).
            svg.style.zIndex = (P.mode === 'plan' && P.editMode) ? '5' : '0';
            const CELL = P.CELL, s = ptsAttr();
            // grid pattern
            const defs = svgEl('defs');
            const pat = svgEl('pattern', { id: 'gpgrid', width: CELL, height: CELL, patternUnits: 'userSpaceOnUse' });
            pat.append(svgEl('path', { d: 'M' + CELL + ' 0H0V' + CELL, fill: 'none', class: 'gp-gridline' }));
            const clip = svgEl('clipPath', { id: 'gpclip' }); clip.append(svgEl('polygon', { points: s }));
            defs.append(pat, clip); svg.append(defs);
            svg.append(svgEl('polygon', { class: 'gp-floorfill', points: s }));
            if (P.grid !== false) svg.append(svgEl('rect', { width: P.Wm * CELL, height: P.Hm * CELL, fill: 'url(#gpgrid)', 'clip-path': 'url(#gpclip)' }));
            svg.append(svgEl('polygon', { class: 'gp-floorstroke', points: s }));
            // edit handles (Garagenplaner + editMode)
            if (P.mode === 'plan' && P.editMode) {
                const n = P.floor.length;
                // Floor centroid (metres) → offset edge labels/handles INWARD so they
                // aren't clipped when an edge sits on the canvas boundary.
                let cX = 0, cY = 0, cA = 0; for (let k = 0; k < n; k++) { const [x0, y0] = P.floor[k], [x1, y1] = P.floor[(k + 1) % n]; const cr = x0 * y1 - x1 * y0; cA += cr; cX += (x0 + x1) * cr; cY += (y0 + y1) * cr; } if (Math.abs(cA) > 1e-6) { cX /= (3 * cA); cY /= (3 * cA); } else { cX = 0; cY = 0; P.floor.forEach(([x, y]) => { cX += x; cY += y; }); cX /= n; cY /= n; }
                for (let i = 0; i < n; i++) {
                    const a = P.floor[i], b = P.floor[(i + 1) % n];
                    const hit = svgEl('line', { class: 'gp-edgehit', 'data-edge': i, x1: a[0] * CELL, y1: a[1] * CELL, x2: b[0] * CELL, y2: b[1] * CELL, stroke: 'transparent', 'stroke-width': 14 }); hit.style.cursor = 'move'; svg.append(hit);
                    const vis = svgEl('line', { 'data-edge': i, x1: a[0] * CELL, y1: a[1] * CELL, x2: b[0] * CELL, y2: b[1] * CELL, class: i === P.selEdge ? 'gp-edge sel' : 'gp-edge' }); vis.style.pointerEvents = 'none'; svg.append(vis);
                    const emx = (a[0] + b[0]) / 2, emy = (a[1] + b[1]) / 2, mx = emx * CELL, my = emy * CELL;
                    // Offset the label PERPENDICULAR to the edge (not toward the centroid —
                    // for concave shapes that pushes it ALONG the line into the "+"), on the
                    // side that points inward (toward the centroid).
                    const ex = b[0] - a[0], ey = b[1] - a[1]; let nx = -ey, ny = ex; const nl = Math.hypot(nx, ny) || 1; nx /= nl; ny /= nl;
                    if ((cX - emx) * nx + (cY - emy) * ny < 0) { nx = -nx; ny = -ny; }
                    const tl = svgEl('text', { x: mx + nx * 26, y: my + ny * 26 + 3, 'text-anchor': 'middle', class: i === P.selEdge ? 'gp-edgelab sel' : 'gp-edgelab' }); tl.style.pointerEvents = 'none'; tl.textContent = edgeLen(i).toFixed(2) + ' m'; svg.append(tl);
                    // Clickable "+" near the edge midpoint (offset inward) to insert a point.
                    const addg = svgEl('g', { class: 'gp-edgeadd', 'data-add': i, transform: 'translate(' + mx + ' ' + my + ')' }); // on the line; label sits inward, so they don't overlap
                    addg.append(svgEl('circle', { r: 7 }), svgEl('path', { d: 'M-3.5 0H3.5M0 -3.5V3.5' }));
                    svg.append(addg);
                }
                for (let j = 0; j < n; j++) {
                    svg.append(svgEl('circle', { class: 'gp-vertex', cx: P.floor[j][0] * CELL, cy: P.floor[j][1] * CELL, r: 6, 'data-vi': j }));
                }
                posLen();
            } else { hideLen(); hideVertMenu(); }
        }
        function posLen() {
            if (P.selEdge == null || P.mode !== 'plan' || !P.editMode) { lenBox.style.display = 'none'; return; }
            const n = P.floor.length, a = P.floor[P.selEdge], b = P.floor[(P.selEdge + 1) % n];
            const pw = P.Wm * P.CELL, ph = P.Hm * P.CELL;
            // Clamp inside the plan so the input is never cut off at an edge.
            let lx = (a[0] + b[0]) / 2 * P.CELL, ly = (a[1] + b[1]) / 2 * P.CELL - 18;
            lx = Math.max(52, Math.min(pw - 52, lx)); ly = Math.max(20, Math.min(ph - 8, ly));
            lenBox.style.left = lx + 'px'; lenBox.style.top = ly + 'px'; lenBox.style.display = 'flex';
            if (document.activeElement !== lenIn) lenIn.value = edgeLen(P.selEdge).toFixed(2);
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
                const d = el('div', { class: 'gp-block veh ' + b.status + (eff !== 'rect' ? ' gp-media' : '') + (eff === 'symbol' ? ' gp-symmode' : '') + (b._id === P.sel ? ' sel' : '') + (warn(b) || b._invalid ? ' invalid' : '') + (ctx ? ' dimctx' : '') });
                d.dataset.id = b._id;
                const warnTxt = !inside(b) ? '⚠ außerhalb' : (b._invalid ? '⚠ blockiert' : ((!heightOK(b) || !weightOK(b)) ? '⚠ Maß' : null));
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
                const d = el('div', { class: 'gp-block excl ' + b.kind + (b.id === P.sel ? ' sel' : '') + (ctx ? ' dimctx' : '') });
                d.dataset.id = b.id;
                if (b.rot) d.append(el('div', { class: 'gp-rlabel' }, el('span', { class: 'gp-rl-name' }, b.label)));
                else d.append(el('span', { class: 'gp-lab' }, el('span', { class: 'gp-who' }, b.label)));
                if (b.id === P.sel && interactive(b) && canManageNow) addHandles(d, b.id);
                positionBlock(d, b); layer.append(d);
            });
        }
        function renderMetrics() {
            metrics.innerHTML = '';
            const total = polyArea(P.floor); let ex = 0, ve = 0; P.excl.forEach((b) => ex += b.w * b.h); P.spots.forEach((b) => ve += b.w * b.h);
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
            const seg = el('div', { class: 'gp-seg', title: 'Snap-Verhalten' });
            seg.append(el('button', { class: !P.snap ? 'on' : '', onclick: () => { P.snap = false; renderToolbar(); } }, 'Frei'));
            [['¼ m', 0.25], ['½ m', 0.5], ['1 m', 1]].forEach(([lab, step]) => seg.append(el('button', { class: (P.snap && P.gridStep === step) ? 'on' : '', onclick: () => { P.snap = true; P.gridStep = step; renderToolbar(); } }, lab)));
            toolbar.append(seg);
            // Vehicle display mode (client-side preference, remembered): symbol / photo / rectangle.
            if (P.mode === 'manage') { const dseg = el('div', { class: 'gp-seg', title: 'Fahrzeug-Darstellung' });
                [['◈', 'symbol', 'Symbol'], ['▣', 'foto', 'Foto'], ['▭', 'rect', 'Rechteck']].forEach(([ic, key, t]) => dseg.append(el('button', { class: P.render === key ? 'on' : '', title: t, onclick: () => { P.render = key; try { localStorage.setItem('gp.render', key); } catch (e) { /* private mode */ } renderToolbar(); draw(); } }, ic)));
                toolbar.append(dseg); }
            // Manage custom planner icons (upload / rename / replace / delete) — placed on
            // the toolbar so it's reachable without first selecting a vehicle.
            if (canManageNow && P.mode === 'manage') toolbar.append(tb('🖼 Icons', 'Eigene Planer-Icons verwalten', async () => { await plannerIconsDialog(); try { P.plannerIcons = await api.get('/planner-icons'); } catch (e) { /* keep old */ } draw(); if (P.sel) renderRail(); }));
            if (canManageNow && P.mode === 'manage') { const rb = tb('⟳ Drehen', 'Auswahl drehen', rotateSel); rb.disabled = !(P.sel && P.spots.find((s) => s._id === P.sel)); toolbar.append(rb); }
            if (canManageNow && P.mode === 'plan') toolbar.append(tb('◇ Form bearbeiten', 'Ecken/Kanten bearbeiten', () => { P.editMode = !P.editMode; P.sel = null; if (!P.editMode) { hideLen(); hideVertMenu(); } else toast('Ecke ziehen · Linie klicken = Länge · Doppelklick = Punkt'); draw(); }, P.editMode));
            toolbar.append(tb('−', 'Verkleinern', () => { P.zoom = Math.max(0.5, +(P.zoom - 0.15).toFixed(2)); layout(); }));
            toolbar.append(el('span', { class: 'gp-zoomlbl' }, Math.round((P.zoom || 1) * 100) + '%'));
            toolbar.append(tb('+', 'Vergrößern', () => { P.zoom = Math.min(4, +(P.zoom + 0.15).toFixed(2)); layout(); }));
            toolbar.append(tb('⤢ Passen', 'Einpassen', () => { P.zoom = 1; layout(); }));
            if (canManageNow && P.dirty) { const sv = tb('● Speichern', 'Jetzt speichern (Auto-Save aktiv)', () => doSaveGeom(), false, 'btn-primary'); toolbar.append(sv); }
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
            const label = b.spotLabel || b.label, geometry = { x: round2(b.x), y: round2(b.y), w: round2(b.w), h: round2(b.h), rot: Math.round(b.rot || 0), status: b.status };
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
        function currentGeometry() { return { floor: P.floor, Wm: P.Wm, Hm: P.Hm, tor: P.tor, load: P.load, shape: P.shape, excl: P.excl.map((e) => ({ kind: e.kind, x: round2(e.x), y: round2(e.y), w: round2(e.w), h: round2(e.h), rot: Math.round(e.rot || 0), label: e.label })) }; }
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
            } catch (err) { if (!silent) toast(err.message || 'Speichern fehlgeschlagen', 'error'); P.dirty = true; renderToolbar(); scheduleSave(); }
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
            // shape + dims
            const shapeCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Hallenform & Maße'));
            const shapeGrid = el('div', { class: 'gp-shapegrid' });
            [['rect', 'Rechteck'], ['l', 'L-Form'], ['u', 'U-Form'], ['trap', 'Schräg'], ['step', 'Stufe']].forEach(([k, lab]) => {
                shapeGrid.append(el('button', { class: P.shape === k ? 'on' : '', onclick: () => setShape(k) }, shapeIcon(k), el('span', {}, lab)));
            });
            shapeCard.append(shapeGrid);
            shapeCard.append(el('div', { class: 'muted', style: 'font-size:.7rem;margin-bottom:.5rem' }, 'Größe & Form über „Form bearbeiten": Ecke/Kante ziehen, Kante anklicken = Länge eintippen, „+" auf Kante = Punkt einfügen, Ecke anklicken = löschen. Breite/Tiefe unten ergeben sich daraus.'));
            const grid2 = el('div', { class: 'gp-grid2' });
            // Direct numeric input (type a value) plus −/+ steppers.
            const dstep = (label, which, val, step) => el('div', { class: 'gp-field' }, el('label', {}, label),
                el('div', { class: 'gp-stepper' }, el('button', { onclick: () => setDim(which, -1) }, '−'),
                    el('input', { class: 'gp-stepin num', type: 'number', step, value: val, onchange: (e) => setDimTo(which, e.target.value) }),
                    el('button', { onclick: () => setDim(which, 1) }, '+')));
            // Breite/Tiefe are now a live INFO readout of the actual floor polygon's bounding
            // box — the exact size is set via „Form bearbeiten" (drag edges / edge-length field),
            // so the numbers can no longer silently regenerate (reset) a hand-drawn shape.
            const bb = floorBB(), fw = (bb.maxX - bb.minX), fh = (bb.maxY - bb.minY);
            const info = (label, val) => el('div', { class: 'gp-field' }, el('label', {}, label), el('div', { class: 'gp-inforo num', title: 'Ergibt sich aus der Form — ändern über „Form bearbeiten"' }, val + ' m'));
            grid2.append(
                info('Breite (m)', fw.toFixed(2).replace('.', ',')),
                info('Tiefe (m)', fh.toFixed(2).replace('.', ',')),
                dstep('Torhöhe (m)', 'tor', P.tor.toFixed(1), '0.5'),
                dstep('Bodenlast (t)', 'load', P.load.toFixed(1), '0.5'));
            shapeCard.append(grid2); rail.append(shapeCard);
            // exclusions
            const exCard = el('div', { class: 'gp-rcard card' }, el('h3', {}, 'Ausgenommene Flächen & Tore'));
            const addrow = el('div', { class: 'gp-addrow' });
            [['lane', '+ Fahrstraße'], ['maint', '+ Wartung'], ['wall', '+ Säule/Wand'], ['exit', '+ Notausgang'], ['gate', '+ Tor']].forEach(([k, lab]) => addrow.append(el('button', { class: 'btn btn-sm', onclick: () => addExcl(k) }, lab)));
            exCard.append(addrow);
            const exList = el('div', { class: 'gp-exlist' });
            if (!P.excl.length) exList.append(el('div', { class: 'muted', style: 'font-size:.76rem;margin-top:.4rem' }, 'Noch keine ausgenommenen Flächen.'));
            P.excl.forEach((b) => {
                const it = el('div', { class: 'gp-exitem' + (b.id === P.sel ? ' on' : '') });
                const sw = el('span', { class: 'gp-sw' }); sw.dataset.kind = b.kind;
                it.append(sw, el('span', { style: 'flex:1' }, b.label, ' ', el('span', { class: 'muted num', style: 'font-size:.72rem' }, b.w + '×' + b.h + ' m')),
                    el('button', { class: 'gp-rm', title: 'Entfernen', onclick: (e) => { e.stopPropagation(); removeExcl(b); } }, '×'));
                it.addEventListener('click', () => { P.sel = b.id; draw(); });
                exList.append(it);
            });
            exCard.append(exList);
            const b = P.excl.find((x) => x.id === P.sel);
            if (b) {
                const segd = el('div', { class: 'gp-segd', style: 'margin-top:.6rem' });
                [['lane', 'Fahrstr.'], ['maint', 'Wartung'], ['wall', 'Wand'], ['exit', 'Notausg.'], ['gate', 'Tor']].forEach(([k, lab]) => segd.append(el('button', { class: b.kind === k ? 'on' : '', onclick: () => { b.kind = k; b.label = EXCL[k].label; commitGeom(); } }, lab)));
                exCard.append(segd);
                // Direct size entry (Breite × Tiefe) for the selected zone.
                const sizeRow = el('div', { class: 'gp-grid2', style: 'margin-top:.5rem' },
                    el('div', { class: 'gp-field' }, el('label', {}, 'Breite (m)'), el('input', { class: 'gp-stepin num', type: 'number', step: '0.1', value: b.w, onchange: (e) => setExclSize(b, 'w', e.target.value) })),
                    el('div', { class: 'gp-field' }, el('label', {}, 'Tiefe (m)'), el('input', { class: 'gp-stepin num', type: 'number', step: '0.1', value: b.h, onchange: (e) => setExclSize(b, 'h', e.target.value) })));
                exCard.append(sizeRow, el('div', { class: 'muted', style: 'font-size:.72rem;margin-top:.35rem' }, 'Ecke ziehen = Größe · Ziehen = verschieben.'));
            }
            rail.append(exCard);
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
            if (b.x + cand.w > P.Wm || b.y + cand.h > P.Hm || collide(cand, b.id)) { toast('Passt nicht (Rand/Kollision)', 'error'); draw(); return; }
            b[dim] = nv; commitGeom();
        }
        function setShape(k) { P.shape = k; P.floor = gpShape(k, P.Wm, P.Hm); hideLen(); const out = P.spots.filter((s) => !inside(s)).length; commitGeom('Form: ' + k + (out ? ' · ' + out + ' außerhalb' : ''), out ? 'warn' : ''); }
        function setDim(which, d) {
            if (which === 'w') { P.Wm = Math.max(6, Math.min(40, P.Wm + d)); P.floor = gpShape(P.shape, P.Wm, P.Hm); clampAll(); }
            else if (which === 'h') { P.Hm = Math.max(5, Math.min(30, P.Hm + d)); P.floor = gpShape(P.shape, P.Wm, P.Hm); clampAll(); }
            else if (which === 'tor') P.tor = Math.max(1.5, Math.min(8, +(P.tor + d * 0.5).toFixed(1)));
            else if (which === 'load') P.load = Math.max(0.5, Math.min(60, +(P.load + d * 0.5).toFixed(1)));
            hideLen(); pushUndo(); markDirty(); layout();
        }
        function clampAll() { blocksAll().forEach((b) => { const t = b.kind === 'excl' ? P.excl.find((e) => e.id === b.id) : P.spots.find((s) => s._id === b._id); if (t) { const cl = clampXY(t, t.x, t.y); t.x = cl.x; t.y = cl.y; if (t._id) t._dirty = true; } }); }
        function addExcl(k) {
            const s = EXCL[k]; const b = { id: 'e' + (P.uid++), kind: k, x: 1, y: 1, w: s.w, h: s.h, label: s.label };
            outer: for (let gy = 0; gy < P.Hm - s.h + 1; gy++) { for (let gx = 0; gx < P.Wm - s.w + 1; gx++) { b.x = gx; b.y = gy; if (!collide(b, null)) break outer; } }
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
        function setMode(m) { P.mode = m; root.dataset.mode = m; P.sel = null; P.editMode = false; hideLen(); hideVertMenu(); ctitle.textContent = m === 'plan' ? 'Garagenplaner' : 'Digitaler Zwilling'; rebuildSwitch(); draw(); }
        function toggleMax(force) { P.maxed = force == null ? !P.maxed : force; root.classList.toggle('maxed', P.maxed); maxBtn.textContent = P.maxed ? '⤢' : '⛶'; if (!P.maxed) { rail.style.left = ''; rail.style.top = ''; rail.style.right = ''; } setTimeout(layout, 20); }
        // In fullscreen the rail floats over the canvas and can be dragged by any
        // card header ("dynamisch drüberliegend und verschiebbar").
        let railDrag = null;
        rail.addEventListener('pointerdown', (e) => { if (!P.maxed) return; const h = e.target.closest('.gp-rcard h3'); if (!h) return; const r = rail.getBoundingClientRect(); railDrag = { dx: e.clientX - r.left, dy: e.clientY - r.top }; try { rail.setPointerCapture(e.pointerId); } catch (er) { /* ignore */ } e.preventDefault(); });
        rail.addEventListener('pointermove', (e) => { if (!railDrag) return; rail.style.left = Math.max(4, e.clientX - railDrag.dx) + 'px'; rail.style.top = Math.max(4, e.clientY - railDrag.dy) + 'px'; rail.style.right = 'auto'; });
        rail.addEventListener('pointerup', (e) => { if (railDrag) { railDrag = null; try { rail.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ } } });

        // ---- floor editing (vertices/edges) ----
        let vdrag = null, edrag = null;
        svg.addEventListener('pointerdown', (e) => {
            if (spaceDown) return; // Space held → let planWrap pan
            if (e.button !== 0 || P.mode !== 'plan' || !P.editMode || !canManageNow) return;
            hideVertMenu(); // any canvas interaction closes an open vertex menu
            const ad = e.target.closest('.gp-edgeadd');
            if (ad) { const i = +ad.dataset.add, n = P.floor.length, a = P.floor[i], b = P.floor[(i + 1) % n]; P.floor.splice(i + 1, 0, [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2]); P.selEdge = null; hideLen(); commitGeom('Punkt eingefügt'); e.preventDefault(); return; }
            const vh = e.target.closest('.gp-vertex');
            if (vh) { const vi = +vh.dataset.vi; vdrag = { vi, o: P.floor[vi].slice(), sx: e.clientX, sy: e.clientY, moved: false }; P.selEdge = null; svg.setPointerCapture(e.pointerId); e.preventDefault(); draw(); return; }
            const eg = e.target.closest('.gp-edgehit');
            if (eg) { const i = +eg.dataset.edge; edrag = { i, sx: e.clientX, sy: e.clientY, moved: false, o0: P.floor[i].slice(), o1: P.floor[(i + 1) % P.floor.length].slice() }; svg.setPointerCapture(e.pointerId); e.preventDefault(); }
        });
        svg.addEventListener('pointermove', (e) => {
            const r = planEl.getBoundingClientRect();
            if (vdrag) {
                if (!vdrag.moved && Math.abs(e.clientX - vdrag.sx) + Math.abs(e.clientY - vdrag.sy) < 4) return; // click, not drag
                vdrag.moved = true;
                let x = Math.max(0, Math.min(P.Wm, snapV((e.clientX - r.left) / P.CELL, P.gridStep))), y = Math.max(0, Math.min(P.Hm, snapV((e.clientY - r.top) / P.CELL, P.gridStep)));
                const nn = P.floor.length, pv = P.floor[(vdrag.vi - 1 + nn) % nn], nv = P.floor[(vdrag.vi + 1) % nn];
                if (P.ortho || e.shiftKey) { if (Math.abs(x - pv[0]) < Math.abs(y - pv[1])) x = pv[0]; else y = pv[1]; }
                else {
                    // Magnetic H/V snap: align x to a neighbour's x (vertical edge) or y to a
                    // neighbour's y (horizontal edge) when within ~10px — makes clean shapes easy.
                    const th = 10 / P.CELL, dxp = Math.abs(x - pv[0]), dxn = Math.abs(x - nv[0]), dyp = Math.abs(y - pv[1]), dyn = Math.abs(y - nv[1]);
                    if (dxp <= th && dxp <= dxn) x = pv[0]; else if (dxn <= th) x = nv[0];
                    if (dyp <= th && dyp <= dyn) y = pv[1]; else if (dyn <= th) y = nv[1];
                }
                P.floor[vdrag.vi] = [x, y]; drawFloor(); renderMetrics(); return;
            }
            if (edrag) { if (!edrag.moved && Math.abs(e.clientX - edrag.sx) + Math.abs(e.clientY - edrag.sy) < 4) return; edrag.moved = true;
                let dx = snapV((e.clientX - edrag.sx) / P.CELL, P.gridStep), dy = snapV((e.clientY - edrag.sy) / P.CELL, P.gridStep);
                dx = Math.max(-Math.min(edrag.o0[0], edrag.o1[0]), Math.min(P.Wm - Math.max(edrag.o0[0], edrag.o1[0]), dx));
                dy = Math.max(-Math.min(edrag.o0[1], edrag.o1[1]), Math.min(P.Hm - Math.max(edrag.o0[1], edrag.o1[1]), dy));
                const n = P.floor.length; P.floor[edrag.i] = [edrag.o0[0] + dx, edrag.o0[1] + dy]; P.floor[(edrag.i + 1) % n] = [edrag.o1[0] + dx, edrag.o1[1] + dy]; P.selEdge = edrag.i; drawFloor(); renderMetrics(); }
        });
        svg.addEventListener('pointerup', (e) => {
            try { svg.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ }
            if (vdrag) { const vi = vdrag.vi, moved = vdrag.moved; vdrag = null; if (moved) commitGeom(); else showVertMenu(vi); return; }
            if (edrag) { P.selEdge = edrag.i; if (edrag.moved) { commitGeom('Linie verschoben'); } else { draw(); lenIn.focus(); lenIn.select(); } edrag = null; }
        });
        svg.addEventListener('dblclick', (e) => {
            if (e.button !== 0 || P.mode !== 'plan' || !P.editMode || !canManageNow) return;
            hideVertMenu();
            // Vertex delete now lives in the click menu; keep only the edge dbl-click
            // shortcut to insert a point (alongside the "+" handle).
            const eg = e.target.closest('.gp-edgehit'); if (eg) { const i = +eg.dataset.edge, r = planEl.getBoundingClientRect();
                P.floor.splice(i + 1, 0, [Math.max(0, Math.min(P.Wm, snapV((e.clientX - r.left) / P.CELL, P.gridStep))), Math.max(0, Math.min(P.Hm, snapV((e.clientY - r.top) / P.CELL, P.gridStep)))]); hideLen(); commitGeom('Punkt eingefügt'); }
        });

        // ---- block move / resize / select ----
        let act = null;
        layer.addEventListener('pointerdown', (e) => {
            if (e.button !== 0 || spaceDown) return; // Space held → let planWrap pan
            const rt = e.target.closest('.gp-rot');
            if (rt && canManageNow) { const bb = P.spots.find((x) => x._id == rt.dataset.rotid) || P.excl.find((x) => x.id === rt.dataset.rotid); if (bb) { act = { mode: 'rotate', b: bb, elm: e.target.closest('.gp-block'), cx: bb.x + bb.w / 2, cy: bb.y + bb.h / 2, orot: bb.rot || 0 }; e.target.setPointerCapture(e.pointerId); } return; }
            const rz = e.target.closest('.gp-rz');
            if (rz && canManageNow) { const id = rz.dataset.rz; const bb = P.spots.find((x) => x._id == id) || P.excl.find((x) => x.id === id); if (bb) { act = { mode: 'resize', b: bb, elm: e.target.closest('.gp-block'), dir: rz.dataset.dir, o0: { x: bb.x, y: bb.y, w: bb.w, h: bb.h, rot: bb.rot || 0 } }; e.target.setPointerCapture(e.pointerId); } return; }
            const elm = e.target.closest('.gp-block'); if (!elm) return;
            const id = elm.dataset.id; const b = P.spots.find((x) => x._id == id) || P.excl.find((x) => x.id === id); if (!b) return;
            if (!interactive(b) || !canManageNow) { P.sel = b._id || b.id; draw(); return; }
            const r = planEl.getBoundingClientRect();
            act = { mode: 'move', b, elm, sx: e.clientX, sy: e.clientY, gx: e.clientX - r.left - b.x * P.CELL, gy: e.clientY - r.top - b.y * P.CELL, moved: false, ox: b.x, oy: b.y };
            elm.setPointerCapture(e.pointerId);
        });
        layer.addEventListener('pointermove', (e) => {
            if (!act) return; const r = planEl.getBoundingClientRect(), b = act.b;
            if (act.mode === 'rotate') {
                const wx = (e.clientX - r.left) / P.CELL, wy = (e.clientY - r.top) / P.CELL;
                let ang = Math.atan2(wy - act.cy, wx - act.cx) * 180 / Math.PI + 90; // handle sits above centre
                if (P.snap && !e.altKey) ang = Math.round(ang / 15) * 15; // 15° snap unless Alt = free
                ang = ((ang % 360) + 360) % 360; b.rot = ang;
                act.elm.style.transform = 'rotate(' + ang + 'deg)';
                act.elm.querySelectorAll('.gp-code,.gp-lab,.gp-warnb').forEach((t) => { t.style.transform = 'rotate(' + (-ang) + 'deg)'; }); // keep labels upright while dragging
                act.elm.classList.toggle('invalid', b._id ? !validVeh(b, b._id) : collide(b, b.id)); return;
            }
            if (act.mode === 'resize') {
                const px = (e.clientX - r.left) / P.CELL, py = (e.clientY - r.top) / P.CELL;
                const nr = resizeBlock(act.o0, act.dir, px, py); b.x = nr.x; b.y = nr.y; b.w = nr.w; b.h = nr.h;
                positionBlock(act.elm, b); // left/top/width/height (+ rotation transform)
                const isVeh = !!b._id && b.kind !== 'excl';
                if (isVeh) { b.L = b.w; b.W = b.h; act.elm.querySelectorAll('.gp-dim,.gp-rl-dim').forEach((t) => { t.textContent = dimText(b); }); } // live L×B
                const bad = isVeh ? !validVeh({ kind: 'veh', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b._id) : collide({ kind: 'excl', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b.id);
                act.elm.classList.toggle('invalid', bad); renderMetrics(); return; }
            if (!act.moved && Math.abs(e.clientX - act.sx) + Math.abs(e.clientY - act.sy) < 4) return; act.moved = true; act.elm.classList.add('moving');
            let nx = (e.clientX - r.left - act.gx) / P.CELL, ny = (e.clientY - r.top - act.gy) / P.CELL; nx = gsnap(nx); ny = gsnap(ny);
            const cl = snapPos(b, nx, ny); nx = cl.x; ny = cl.y; b.x = nx; b.y = ny; act.elm.style.left = (nx * P.CELL) + 'px'; act.elm.style.top = (ny * P.CELL) + 'px';
            const isVeh = !!b._id && b.kind !== 'excl'; const bad = isVeh ? !validVeh({ kind: 'veh', x: nx, y: ny, w: b.w, h: b.h, rot: b.rot }, b._id) : collide({ kind: 'excl', x: nx, y: ny, w: b.w, h: b.h }, b.id); act.elm.classList.toggle('invalid', bad);
        });
        layer.addEventListener('pointerup', (e) => {
            if (!act) return; const b = act.b, d = act; act = null;
            try { d.elm.releasePointerCapture(e.pointerId); } catch (er) { /* ignore */ }
            const isVeh = !!b._id && b.kind !== 'excl';
            if (d.mode === 'rotate') {
                const bad = isVeh ? !validVeh(b, b._id) : collide(b, b.id);
                if (bad && !isVeh) { b.rot = d.orot; draw(); toast('Drehen: Kollision/außerhalb — zurück', 'error'); return; } // exclusions still snap back
                P.sel = b._id || b.id;
                if (isVeh) { b._invalid = bad; b._dirty = true; markDirty(); pushUndo(); draw(); toast(bad ? 'Gedreht — ungültige Lage, Speichern erst wenn frei' : 'Gedreht', bad ? 'warn' : ''); }
                else { commitGeom(); toast('Gedreht'); } // commitGeom already redraws
                return;
            }
            if (d.mode === 'resize') {
                const bad = isVeh ? !validVeh({ kind: 'veh', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b._id) : collide({ kind: 'excl', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b.id);
                if (bad) { b.x = d.o0.x; b.y = d.o0.y; b.w = d.o0.w; b.h = d.o0.h; if (isVeh) { b.L = d.o0.w; b.W = d.o0.h; } P.sel = b._id || b.id; draw(); toast('Passt nicht (Rand/Kollision) — zurück', 'error'); return; }
                P.sel = b._id || b.id;
                if (isVeh) { b.L = b.w; b.W = b.h; persistDims(b); b._dirty = true; markDirty(); pushUndo(); } else commitGeom();
                draw(); toast('Größe geändert' + (isVeh ? ' · ' + dimText(b).replace(/^.*· /, '') : '')); return;
            }
            if (!d.moved) { P.sel = b._id || b.id; draw(); return; }
            d.elm.classList.remove('moving');
            const bad = isVeh ? !validVeh({ kind: 'veh', x: b.x, y: b.y, w: b.w, h: b.h, rot: b.rot }, b._id) : collide({ kind: 'excl', x: b.x, y: b.y, w: b.w, h: b.h }, b.id);
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
            // Revert an in-flight floor edit to its pre-drag geometry (no history entry).
            if (vdrag) { P.floor[vdrag.vi] = vdrag.o; vdrag = null; drawFloor(); renderMetrics(); }
            if (edrag) { const n = P.floor.length; P.floor[edrag.i] = edrag.o0; P.floor[(edrag.i + 1) % n] = edrag.o1; edrag = null; drawFloor(); renderMetrics(); }
            if (act) { const b = act.b; if (act.mode === 'move') { b.x = act.ox; b.y = act.oy; } else if (act.mode === 'resize') { b.w = act.ow; b.h = act.oh; } else if (act.mode === 'rotate') { b.rot = act.orot; } if (act.elm) act.elm.classList.remove('moving'); act = null; draw(); }
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

        // initial paint
        setMode(P.mode);
        pushUndo();
        requestAnimationFrame(() => { layout(); if (focusTarget != null) requestAnimationFrame(() => focusSpot(focusTarget)); });
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

    async function init() {
        initTheme();
        applyBrand();
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
