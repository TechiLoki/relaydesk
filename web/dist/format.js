export function escapeHTML(value) {
    return String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] ?? c));
}
export function money(amount, currency) {
    try {
        const f = new Intl.NumberFormat('en-US', { style: 'currency', currency });
        const decimals = f.resolvedOptions().maximumFractionDigits ?? 2;
        return f.format(amount / 10 ** decimals);
    }
    catch {
        return `${amount} ${currency} (minor units)`;
    }
}
export function label(status) {
    return { pending: 'Queued', processing: 'Delivering', retry_wait: 'Retry scheduled', succeeded: 'Delivered', failed: 'Needs attention' }[status] ?? status;
}
export function validStatus(status) {
    return ['pending', 'processing', 'retry_wait', 'succeeded', 'failed'].includes(status) ? status : 'pending';
}
export function age(value) {
    if (!value)
        return 'Not yet';
    const seconds = Math.max(0, Math.floor((Date.now() - Date.parse(value)) / 1000));
    if (!Number.isFinite(seconds))
        return 'Unknown';
    if (seconds < 60)
        return `${seconds}s ago`;
    if (seconds < 3600)
        return `${Math.floor(seconds / 60)}m ago`;
    return new Date(value).toLocaleString();
}
