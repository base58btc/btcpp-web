function closeMenu(el) {
	document.querySelectorAll('[role="nav-dialog"]').forEach(function (el){
		el.classList.add("hidden");
	})
}

function toggleMenu(el) {
	document.querySelectorAll('[role="nav-dialog"]').forEach(function (el){
		if (el.classList.contains("hidden")) {
			el.classList.remove("hidden");
		} else {
			el.classList.add("hidden");
		}
	});

	return true;
}

function toggleMobileFlyout(el, select) {
	document.querySelectorAll('[role="mobile-flyout-' + select + '"]').forEach(function (el){
		if (el.classList.contains("hidden")) {
			el.classList.remove("hidden");
		} else {
			el.classList.add("hidden");
		}
	});
	document.querySelectorAll('[role="nav-caret-' + select + '"]').forEach(function (el){
		if (el.classList.contains("rotate-180")) {
			el.classList.remove("rotate-180");
		} else {
			el.classList.add("rotate-180");
		}
	});

	return true;
}

// Global submit spinner: shows a full-page overlay whenever a form posts back
// to the server, so the user gets feedback on round-trip operations. The
// overlay is created lazily and hides itself on pageshow (covers bfcache).
document.addEventListener("DOMContentLoaded", function () {
	if (document.getElementById("global-submit-overlay")) return;
	var overlay = document.createElement("div");
	overlay.id = "global-submit-overlay";
	overlay.innerHTML = '<div class="global-submit-spinner"></div>';
	document.body.appendChild(overlay);

	document.addEventListener("submit", function (e) {
		// Don't show on HTMX-driven submits — they have their own indicators.
		var form = e.target;
		if (form && (form.hasAttribute("hx-post") || form.hasAttribute("hx-get") ||
			form.hasAttribute("hx-put") || form.hasAttribute("hx-delete"))) {
			return;
		}
		overlay.classList.add("active");
	}, true);
});

// Hide overlay if the user comes back via the back button (bfcache).
window.addEventListener("pageshow", function () {
	var overlay = document.getElementById("global-submit-overlay");
	if (overlay) overlay.classList.remove("active");
});

function toggleNavFlyout(el, targetId) {
	// When called with a targetId, toggle just that flyout — and close
	// any siblings so two flyouts can't be open at once. Without an ID
	// we keep the legacy "toggle all" behaviour.
	var nodes = document.querySelectorAll('[role="nav-flyout"]');
	nodes.forEach(function (node) {
		if (targetId && node.id !== targetId) {
			node.classList.remove("transition-in");
			node.classList.add("transition-out");
			node.style.transform = "translateY(-100%)";
			node.style.opacity = 0;
			return;
		}
		if (node.classList.contains("transition-in")) {
			node.classList.remove("transition-in");
			node.classList.add("transition-out");
			node.style.transform = "translateY(-100%)";
			node.style.opacity = 0;
		} else {
			node.classList.remove("transition-out");
			node.classList.add("transition-in");
			node.style.transform = "translateY(0%)";
			node.style.opacity = 1;
		}
	});

	return true;
}

// Conf-page countdown ticker. Each .conf-countdown element carries
// data-start / data-end as Unix seconds (rendered by the
// conf_countdown template). Before doors open: positive countdown.
// Between doors open and close: 0d 00h 00m 00s. After doors close:
// negative count. Runs site-wide; no-ops on pages without any
// .conf-countdown elements.
(function () {
	function pad(n) { return String(n).padStart(2, '0'); }
	function tick(el) {
		var startSec = Number(el.dataset.start);
		var endSec   = Number(el.dataset.end);
		var valueEl  = el.querySelector('[data-cd-value]');
		var prefixEl = el.querySelector('[data-cd-prefix]');
		var now = Date.now() / 1000;
		var prefix, sign, abs;
		if (now < startSec) {
			prefix = 'doors open in';
			sign = '';
			abs = startSec - now;
		} else if (now > endSec) {
			prefix = 'doors closed';
			sign = '-';
			abs = now - endSec;
		} else {
			prefix = 'happening now ·';
			sign = '';
			abs = 0;
		}
		var d = Math.floor(abs / 86400);
		var h = Math.floor((abs % 86400) / 3600);
		var m = Math.floor((abs % 3600) / 60);
		var s = Math.floor(abs % 60);
		if (prefixEl) prefixEl.textContent = prefix;
		if (valueEl)  valueEl.textContent  = sign + d + 'd ' + pad(h) + 'h ' + pad(m) + 'm ' + pad(s) + 's';
	}
	function tickAll() {
		document.querySelectorAll('.conf-countdown').forEach(tick);
	}
	function init() {
		var els = document.querySelectorAll('.conf-countdown');
		if (els.length === 0) return;
		tickAll();
		setInterval(tickAll, 1000);
	}
	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', init);
	} else {
		init();
	}
})();
