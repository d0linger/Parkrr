'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Exercise the SPA's actual response and logout functions without a live backend.
const source = fs.readFileSync(path.join(__dirname, '../../web/static/js/app.js'), 'utf8');
function section(start, end) {
    const from = source.indexOf(start);
    const to = source.indexOf(end, from);
    assert.ok(from >= 0 && to > from, 'SPA test section exists');
    return source.slice(from, to);
}
function forbidden(error) {
    return { status: 403, ok: false, headers: new Map([['content-type', 'application/json']]),
        json: async () => ({ error }) };
}

test('concurrent MFA failures log out and warn once, then reset for the next session', async () => {
    let logoutCalls = 0;
    let loginViews = 0;
    let releaseLogout;
    let logoutStarted;
    const warnings = [];
    const redirects = [];
    const state = { user: { id: 1 } };
    const api = { post: async (url) => {
        assert.equal(url, '/auth/logout');
        logoutCalls++;
        logoutStarted();
        await new Promise((resolve, reject) => { releaseLogout = { resolve, reject }; });
    } };
    const handlers = vm.runInNewContext(
        section('    let twoFARedirected =', '    // ---------- state ----------') +
        section('    async function logout()', '    // ---------- passkeys (WebAuthn) ----------') +
        '\n({ handle, logout });',
        { state, api, totalCounts: new Map(), listState: new Map(), bulkSel: new Set(),
            bulkMode: false, dashYear: null, calMonth: null,
            showLogin: () => { loginViews++; },
            toast: (message) => warnings.push(message), navigate: (page) => redirects.push(page) },
    );
    for (let session = 1; session <= 2; session++) {
        state.user = { id: session };
        for (let i = 0; i < 2; i++) {
            await assert.rejects(handlers.handle(forbidden('2fa_enrollment_required')), /2fa_enrollment_required/);
        }
        assert.equal(redirects.length, session, 'enrollment redirect resets on logout');
        warnings.length = 0;
        const started = new Promise((resolve) => { logoutStarted = resolve; });
        const pending = Promise.allSettled(Array.from({ length: 20 }, () =>
            handlers.handle(forbidden('2fa_authentication_required'))));
        await started;
        assert.equal(logoutCalls, session, 'only one logout is in flight');
        assert.equal(warnings.length, 0, 'warning waits for logout');
        if (session === 1) releaseLogout.resolve();
        else releaseLogout.reject(new Error('logout network failure'));
        const results = await pending;
        assert.ok(results.every((r) => r.status === 'rejected' && r.reason.status === 403));
        assert.equal(state.user, null);
        assert.equal(loginViews, session);
        assert.equal(logoutCalls, session);
        assert.equal(warnings.length, 1, 'only one MFA warning per session');
        await assert.rejects(handlers.handle(forbidden('2fa_authentication_required')), /2fa_authentication_required/);
        assert.equal(logoutCalls, session, 'late failures do not log out again');
        assert.equal(warnings.length, 1);
    }
});
