// Notification system: stacked toasts with auto-dismiss, progress bar,
// max visible limit, and proper queuing.
//
// Error toasts can optionally include a Retry button (passed by the
// actions framework when an action def sets `retryable`). Clicking the
// button invokes the supplied callback and dismisses the toast.

import { el } from './dom.js';

const MAX_VISIBLE = 3;

let container: HTMLElement | null = null;
const queue: HTMLElement[] = [];

function ensureContainer(): HTMLElement {
  if (container) return container;
  container = el('div', {
    className: 'toast-stack',
    'aria-live': 'polite',
    role: 'status'
  });
  document.body.appendChild(container);
  return container;
}

function show(message: string, level: string, duration: number, retry?: { onClick: () => void }): void {
  const c = ensureContainer();
  const toast = el('div', {
    className: 'toast',
    'data-level': level
  }, message);

  if (retry !== undefined) {
    // Render a Retry button inside the toast. Click invokes the callback
    // and dismisses the toast. Stop propagation so the toast's own click
    // handler doesn't double-dismiss.
    const btn = el('button', {
      type: 'button',
      className: 'toast-retry',
      'aria-label': 'Retry',
    }, 'Retry');
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      retry.onClick();
      dismiss(toast);
    });
    toast.appendChild(btn);
  }

  if (duration > 0) {
    const bar = el('span', { className: 'toast-progress' });
    bar.style.animationDuration = `${duration}ms`;
    toast.appendChild(bar);
  }
  toast.dataset['duration'] = String(duration);

  toast.addEventListener('click', () => dismiss(toast), { once: true });

  if (c.children.length >= MAX_VISIBLE) {
    queue.push(toast);
    return;
  }

  c.appendChild(toast);

  if (duration > 0) {
    setTimeout(() => dismiss(toast), duration);
  }
}

function dismiss(toast: HTMLElement): void {
  toast.classList.add('toast-exit');
  let removed = false;
  const remove = () => {
    if (removed) return;
    removed = true;
    toast.remove();
    if (queue.length > 0 && container && container.children.length < MAX_VISIBLE) {
      const next = queue.shift();
      if (next) {
        container.appendChild(next);
        const ms = Number(next.dataset['duration'] || 0);
        if (ms > 0) setTimeout(() => dismiss(next), ms);
      }
    }
  };
  toast.addEventListener('animationend', remove, { once: true });
  setTimeout(() => { if (toast.parentNode) remove(); }, 400);
}

export function success(message: string): void { show(message, 'ok', 4000); }

/** Error toast. Optional retry callback renders a Retry button inside the
 *  toast — used by the actions framework when an action def sets
 *  `retryable`. */
export function error(message: string, retry?: { onClick: () => void }): void {
  show(message, 'err', 0, retry);
}

export function info(message: string): void { show(message, 'info', 3000); }
