import { escapeHTML as esc, money, label, validStatus, age } from './format.js';
const $ = (id) => { const el = document.getElementById(id); if (!el)
    throw new Error('Missing element: ' + id); return el; };
const state = { view: 'overview', overview: null, orders: [], total: 0, offset: 0, search: '', status: '', polling: false, authenticated: false, detailID: null, selection: 0 };
let toastTimer;
function toast(message) { $('toast').textContent = message; $('toast').hidden = false; window.clearTimeout(toastTimer); toastTimer = window.setTimeout(() => $('toast').hidden = true, 5000); }
function showLogin() { state.authenticated = false; const dialog = $('login-dialog'); if (!dialog.open)
    dialog.showModal(); }
async function request(path, data) {
    const response = await fetch(path, { ...(data === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Relaydesk-Request': '1' }, body: JSON.stringify(data) }), credentials: 'same-origin' });
    let result;
    try {
        result = await response.json();
    }
    catch {
        throw new Error('Service returned an invalid response. Check that the API is running.');
    }
    if (response.status === 401 && path != '/api/login')
        showLogin();
    if (!response.ok) {
        const error = result;
        throw new Error(error.error ?? 'Request failed');
    }
    return result;
}
async function action(button, task) {
    if (button.disabled)
        return;
    button.disabled = true;
    try {
        await task();
    }
    catch (error) {
        toast(error instanceof Error ? error.message : String(error));
    }
    finally {
        button.disabled = false;
        renderControls();
    }
}
function badge(status) { return `<span class="status-badge ${validStatus(status)}">${esc(label(status))}</span>`; }
function setConnected(connected) { $('connection-dot').classList.toggle('offline', !connected); $('connection-label').textContent = connected ? 'Connected' : 'Reconnecting'; }
function renderControls() {
    const hasDemo = state.authenticated && Boolean(state.overview?.has_demo);
    $('replay-event').disabled = !hasDemo;
    $('update-order').disabled = !hasDemo;
    $('previous-page').disabled = state.offset === 0;
    $('next-page').disabled = state.offset + 25 >= state.total;
}
function renderOverview(data) {
    state.overview = data;
    for (const [id, key] of [['metric-orders', 'orders'], ['metric-delivered', 'delivered'], ['metric-attention', 'attention'], ['metric-duplicates', 'duplicates']])
        $(id).textContent = Number(data[key]).toLocaleString();
    $('nav-count').textContent = String(data.orders);
    $('destination-mode').value = data.mode;
    $('destination-state').textContent = { healthy: 'Ready to receive', outage: 'Simulated 503 outage', lost_ack: 'First acknowledgement lost' }[data.mode] ?? data.mode;
    $('lab-note').textContent = data.mode === 'outage' ? 'The simulator returns 503 without saving orders. Restore Healthy to let scheduled retries recover.' : data.mode === 'lost_ack' ? 'The next new delivery is saved, but its first reply is a 503. Its retry reuses the same idempotency key.' : 'Replay an event to prove that duplicate webhooks do not create another delivery.';
    $('worker-dot').classList.toggle('offline', !data.worker_ready);
    $('worker-status').textContent = data.worker_ready ? 'Worker polling · ' + data.queued + ' queued' : 'Worker offline or still starting';
    renderControls();
}
function renderOrders(page) {
    state.orders = page.orders;
    state.total = page.total;
    $('order-total').textContent = String(page.total);
    $('orders-body').innerHTML = page.orders.map(o => `<tr><td><button class="order-link" data-order="${esc(o.id)}">${esc(o.external_id)}</button><div class="source-label"><span>${esc(o.source)}</span> version ${o.version}</div></td><td><strong>${esc(o.payload.customer || 'Checkout customer')}</strong><small>${esc(o.payload.email || 'No email provided')}</small></td><td><strong>${esc(money(o.payload.amount_minor, o.payload.currency))}</strong><small>${esc(o.payload.currency)}</small></td><td>${badge(o.status)}</td><td>${o.attempt_count}<small>${o.attempt_count === 1 ? 'request' : 'requests'}</small></td><td>${esc(age(o.updated_at))}</td><td><button class="icon-button" data-order="${esc(o.id)}" aria-label="Inspect ${esc(o.external_id)}">↗</button></td></tr>`).join('');
    $('empty-orders').hidden = page.orders.length > 0;
    $('page-summary').textContent = page.total ? `Showing ${state.offset + 1}–${Math.min(state.offset + 25, page.total)} of ${page.total} orders` : 'No orders found';
    renderControls();
}
function eventRows(events) {
    return events.map(event => `<div class="event-row"><span class="event-icon">${event.action.includes('succeeded') ? '✓' : event.action.includes('duplicate') ? '◇' : '↗'}</span><div><strong>${esc(event.action.replaceAll('_', ' '))}${event.external_id ? ' · ' + esc(event.external_id) : ''}</strong><p>${esc(Object.entries(event.detail ?? {}).filter(([, v]) => v !== null).map(([key, value]) => `${key.replaceAll('_', ' ')}: ${String(value)}`).join(' · '))}</p></div><time datetime="${esc(event.created_at)}">${esc(age(event.created_at))}</time></div>`).join('') || '<p class="empty-small">Events will appear when the first order arrives.</p>';
}
function renderDeliveries(deliveries) {
    $('alternate-content').innerHTML = `<div class="table-scroll"><table><thead><tr><th>ORDER / VERSION</th><th>STATUS</th><th>ATTEMPTS</th><th>NEXT ATTEMPT</th><th></th></tr></thead><tbody>${deliveries.map(d => `<tr><td><button class="order-link" data-order="${esc(d.order_id)}">${esc(d.external_id)}</button><small>v${d.version} · ${esc(d.id.slice(0, 8))}</small></td><td>${badge(d.status)}${d.last_error ? `<small>${esc(d.last_error)}</small>` : ''}</td><td>${d.attempt_count}</td><td>${d.status === 'retry_wait' ? esc(new Date(d.next_attempt_at).toLocaleTimeString()) : '—'}</td><td>${d.status === 'failed' ? `<button class="button small secondary" data-retry="${esc(d.id)}">↻ Retry</button>` : ''}</td></tr>`).join('')}</tbody></table>${deliveries.length ? '' : '<p class="empty-small">No deliveries yet. Send a sample order to get started.</p>'}</div>`;
}
function renderConnections() {
    const stripe = Boolean(state.overview?.stripe_enabled);
    $('alternate-content').innerHTML = `<article class="connection-card"><h3>Demo store → Fulfillment simulator</h3><p>A complete local integration using synthetic customer data. The source signs each webhook. The destination stores idempotency receipts and ignores stale versions.</p><code>POST ${esc(location.origin)}/webhooks/demo\nRelay-Signature: t=TIMESTAMP,v1=HMAC_SHA256</code><p>Use the demo controls for ready-to-run examples, or follow the signing example in the README.</p></article><article class="connection-card"><h3>Stripe Checkout · test mode ${stripe ? '✓ Configured' : '· Not configured'}</h3><p>Processes paid checkout.session.completed and checkout.session.async_payment_succeeded snapshot events. Live-mode events are rejected. Other event types and unpaid sessions are acknowledged without delivery.</p><code>stripe listen --events checkout.session.completed,checkout.session.async_payment_succeeded --forward-to localhost:8090/webhooks/stripe</code><p>Set STRIPE_WEBHOOK_SECRET in your .env using the listener's whsec_ secret, then recreate the API container. Stripe snapshots include the total, not the full line-item list; the inspector makes that distinction explicit.</p></article><article class="connection-card"><h3>Connect your own destination</h3><p>Configure DESTINATION_URL and DESTINATION_TOKEN. The receiver must accept normalized JSON, persist Idempotency-Key with its business write in one transaction, and return a 2xx acknowledgement. A generic API without that guarantee can still create duplicates after a lost reply.</p><p>Shipping, tax, discounts, and amounts are integer currency minor units. Source versions must increase monotonically.</p></article>`;
}
function renderGuide() {
    $('alternate-content').innerHTML = `<article class="connection-card"><h3>1. Prove duplicate prevention</h3><ol><li>Keep the destination Healthy and send a sample order.</li><li>Replay its webhook two or three times.</li><li>Open the order. Its duplicate count increases, but it has one version and one delivery.</li></ol></article><article class="connection-card"><h3>2. Recover from an outage</h3><ol><li>Select 503 outage and send a new order.</li><li>Open its attempt history. You will see backoff and scheduled retries.</li><li>Restore Healthy. A pending retry will deliver it. If all five attempts ran out, use Retry on the failed delivery.</li></ol></article><article class="connection-card"><h3>3. Test an ambiguous result</h3><ol><li>Select Commit, then lose first reply and send a new order.</li><li>The destination saves the order but responds with 503 once.</li><li>The retry uses the same idempotency key and receives the existing receipt. You get one destination order and one destination commit.</li></ol></article><article class="connection-card"><h3>4. Keep newer data safe</h3><p>Use Update last order to create a new event and source version. Old deliveries cannot overwrite a newer version in the simulator. Previous versions and attempts remain visible in the inspector.</p></article><p class="note">This is a local, single-user portfolio MVP. The demo controls affect all traffic to the simulated destination. They do not alter an external destination configured through the environment. New source versions are queued independently; failed older versions remain inspectable even if a newer version is delivered.</p>`;
}
const titles = { overview: ['Overview', 'A clear path from paid to fulfilled.', 'Every event accounted for. Every delivery traceable.'], orders: ['Orders', 'Every order, one source of truth.', 'Search orders and inspect their latest synchronization state.'], deliveries: ['Delivery log', 'Nothing disappears between systems.', 'Inspect versions, attempt counts, and failed deliveries.'], activity: ['Activity', 'The complete paper trail.', 'A chronological record of ingestion, retries, and destination commits.'], connections: ['Connections', 'Two systems. One dependable connection.', 'Start with the simulator, then connect your own endpoints.'], guide: ['Demo guide', 'Break it. Watch it recover.', 'Four short experiments that demonstrate the delivery guarantees.'] };
function setView(view) {
    state.view = view in titles ? view : 'overview';
    state.offset = 0;
    const title = titles[state.view] ?? titles.overview;
    $('breadcrumb-title').textContent = title[0];
    $('page-title').textContent = title[1];
    $('page-description').textContent = title[2];
    $('metrics').hidden = !['overview', 'orders'].includes(state.view);
    $('demo-lab').hidden = state.view !== 'overview';
    $('orders-panel').hidden = !['overview', 'orders'].includes(state.view);
    $('alternate-panel').hidden = ['overview', 'orders'].includes(state.view);
    $('alternate-title').textContent = title[0];
    $('alternate-description').textContent = title[2];
    $('table-title').firstChild.textContent = state.view === 'orders' ? 'All orders ' : 'Recent orders ';
    document.querySelectorAll('[data-view]').forEach(button => button.classList.toggle('active', button.dataset.view === state.view));
    document.body.classList.remove('menu-open');
    if (state.view === 'connections')
        renderConnections();
    if (state.view === 'guide')
        renderGuide();
    void refresh();
}
async function refresh() {
    if (state.polling)
        return;
    state.polling = true;
    const view = state.view, selection = state.selection;
    try {
        const overview = await request('/api/overview');
        state.authenticated = true;
        renderOverview(overview);
        setConnected(true);
        $('page-error').hidden = true;
        if (['overview', 'orders'].includes(view)) {
            const query = new URLSearchParams({ search: state.search, status: state.status, offset: String(state.offset) });
            const page = await request('/api/orders?' + query);
            if (state.view === view)
                renderOrders(page);
        }
        else if (view === 'deliveries') {
            const rows = await request('/api/deliveries');
            if (state.view === view)
                renderDeliveries(rows);
        }
        else if (view === 'activity') {
            const rows = await request('/api/activity');
            if (state.view === view)
                $('alternate-content').innerHTML = eventRows(rows);
        }
        if (view === 'connections' && state.view === view)
            renderConnections();
        if (state.detailID && $('order-dialog').open) {
            const id = state.detailID;
            const detail = await request('/api/orders/' + id);
            if (state.detailID === id && state.selection === selection)
                renderDetail(detail);
        }
        $('last-updated').textContent = 'Updated ' + new Date().toLocaleTimeString();
    }
    catch (error) {
        setConnected(false);
        if (state.authenticated) {
            $('page-error').textContent = error instanceof Error ? error.message : String(error);
            $('page-error').hidden = false;
        }
    }
    finally {
        state.polling = false;
    }
}
function renderDetail(detail) {
    const order = detail.order;
    const latest = detail.deliveries[0];
    $('detail-title').textContent = order.external_id;
    const duplicateCount = detail.events.reduce((sum, e) => sum + e.duplicate_count, 0);
    const native = order.payload.validation;
    const rawOpen = Boolean($('detail-content').querySelector('details[open]'));
    $('detail-content').innerHTML = `${badge(latest?.status ?? 'pending')}<div class="detail-grid"><div><span>CUSTOMER</span><strong>${esc(order.payload.customer || 'Checkout customer')}</strong></div><div><span>TOTAL</span><strong>${esc(money(order.payload.amount_minor, order.payload.currency))}</strong></div><div><span>SOURCE / VERSION</span><strong>${esc(order.source)} / ${order.version}</strong></div><div><span>DUPLICATE WEBHOOKS</span><strong>${duplicateCount}</strong></div></div><div class="detail-note ${native.checked_lines ? '' : 'warning'}">${native.checked_lines ? `C++ validation passed: ${native.units} units; line items + shipping + tax − discount match the declared total.` : 'Total validated within supported bounds. Stripe snapshot has no line-item list, so item-by-item arithmetic was not verified.'}</div><h3>Destination record</h3>${detail.destination ? `<div class="detail-grid"><div><span>DESTINATION ID</span><strong>${esc(detail.destination.id)}</strong></div><div><span>DESTINATION VERSION</span><strong>${detail.destination.version}</strong></div></div>` : '<p class="empty-small">No destination record yet.</p>'}<h3>Delivery versions</h3>${detail.deliveries.map(d => `<div class="attempt-card"><div><strong>Version ${d.version} · ${d.attempt_count} attempts</strong><small>Idempotency key: ${esc(d.id)}</small>${d.last_error ? `<small>${esc(d.last_error)}</small>` : ''}</div><div>${badge(d.status)}${d.status === 'failed' ? ` <button class="button small secondary" data-retry="${esc(d.id)}">Retry</button>` : ''}</div></div>`).join('')}<h3>Completed request attempts</h3>${detail.attempts.map(a => `<div class="attempt-card"><div><strong>Attempt ${a.number} · ${a.http_status ? 'HTTP ' + a.http_status : 'Network failure'} · ${a.duration_ms} ms</strong><small>${esc(a.error ?? 'Destination acknowledged the delivery')}</small><small>${esc(a.delivery_id.slice(0, 8))} · ${esc(age(a.created_at))}</small></div><span class="status-badge ${a.error ? 'failed' : 'succeeded'}">${a.error ? 'Failed' : 'Accepted'}</span></div>`).join('') || '<p class="empty-small">No completed requests yet. A worker may still be processing.</p>'}<h3>Audit trail</h3>${eventRows(detail.audit)}<details ${rawOpen ? 'open' : ''}><summary>Normalized order JSON</summary><pre>${esc(JSON.stringify(order.payload, null, 2))}</pre></details>`;
}
async function openOrder(id) {
    const selection = ++state.selection;
    state.detailID = id;
    const dialog = $('order-dialog');
    $('detail-title').textContent = 'Loading order…';
    $('detail-content').textContent = 'Fetching the delivery history.';
    if (!dialog.open)
        dialog.showModal();
    try {
        const detail = await request('/api/orders/' + id);
        if (state.selection === selection)
            renderDetail(detail);
    }
    catch (error) {
        if (state.selection === selection)
            $('detail-content').textContent = error instanceof Error ? error.message : String(error);
    }
}
$('login-form').addEventListener('submit', event => {
    event.preventDefault();
    const button = $('login-form').querySelector('button');
    void action(button, async () => {
        $('login-error').textContent = '';
        try {
            await request('/api/login', { token: $('login-token').value });
            $('login-token').value = '';
            $('login-dialog').close();
            state.authenticated = true;
            await refresh();
        }
        catch (error) {
            $('login-error').textContent = error instanceof Error ? error.message : String(error);
        }
    });
});
$('login-dialog').addEventListener('cancel', event => event.preventDefault());
$('logout').onclick = () => void action($('logout'), async () => { await request('/api/logout', {}); showLogin(); });
$('mobile-menu').onclick = () => document.body.classList.toggle('menu-open');
$('refresh').onclick = () => void refresh();
$('send-order').onclick = () => void action($('send-order'), async () => { await request('/api/demo', { action: 'new' }); toast('Signed sample order accepted. Watch its delivery below.'); if (state.view !== 'overview')
    setView('overview');
else
    await refresh(); });
$('replay-event').onclick = () => void action($('replay-event'), async () => { await request('/api/demo', { action: 'replay' }); toast('Webhook replay acknowledged; no second delivery queued.'); await refresh(); });
$('update-order').onclick = () => void action($('update-order'), async () => { await request('/api/demo', { action: 'update' }); toast('A new order version has been validated and queued.'); await refresh(); });
$('destination-mode').onchange = () => { const select = $('destination-mode'); select.disabled = true; void request('/api/simulator', { mode: select.value }).then(() => refresh()).catch(error => toast(String(error))).finally(() => select.disabled = false); };
$('all-orders').onclick = () => setView('orders');
let searchTimer;
$('search').oninput = () => { window.clearTimeout(searchTimer); searchTimer = window.setTimeout(() => { state.search = $('search').value; state.offset = 0; void refresh(); }, 250); };
$('status-filter').onchange = () => { state.status = $('status-filter').value; state.offset = 0; void refresh(); };
$('previous-page').onclick = () => { state.offset = Math.max(0, state.offset - 25); void refresh(); };
$('next-page').onclick = () => { state.offset += 25; void refresh(); };
document.addEventListener('click', event => {
    const target = event.target;
    if (!(target instanceof Element))
        return;
    const view = target.closest('[data-view]');
    if (view)
        setView(view.dataset.view ?? 'overview');
    const order = target.closest('[data-order]');
    if (order)
        void openOrder(order.dataset.order ?? '');
    const close = target.closest('.close-dialog');
    if (close) {
        state.detailID = null;
        state.selection++;
        close.closest('dialog')?.close();
    }
    const retry = target.closest('[data-retry]');
    if (retry)
        void action(retry, async () => { await request('/api/deliveries/' + retry.dataset.retry + '/retry', {}); toast('Failed delivery queued for a new retry cycle.'); await refresh(); });
});
$('order-dialog').addEventListener('close', () => { state.detailID = null; state.selection++; });
void refresh();
window.setInterval(() => { if (state.authenticated)
    void refresh(); }, 2000);
