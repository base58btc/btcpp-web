// cal-picker.js — provider dropdown for the "Add to calendar" badge.
// Reads RFC 3339 timestamps + event metadata from data-* attrs on a
// [data-cal-trigger] button and assembles per-provider URLs on click:
//   - Google Calendar: calendar.google.com deeplink
//   - Outlook.com:     outlook.live.com deeplink
//   - Office 365:      outlook.office.com deeplink
//   - Apple / .ics:    falls through to the existing server-rendered
//                      .ics download URL (data-cal-ics)
//
// Template is in templates/section/cal_picker.tmpl. Behavior here is
// shared across the three button sites (agenda, dashboard talks,
// dashboard shifts).

(function () {
  // toGCalDate: RFC 3339 → Google Calendar's compact UTC form.
  // "2026-04-15T14:00:00-05:00" → "20260415T190000Z"
  function toGCalDate(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    function pad(n) { return String(n).padStart(2, '0'); }
    return d.getUTCFullYear()
      + pad(d.getUTCMonth() + 1)
      + pad(d.getUTCDate())
      + 'T'
      + pad(d.getUTCHours())
      + pad(d.getUTCMinutes())
      + pad(d.getUTCSeconds())
      + 'Z';
  }

  function buildURLs(btn) {
    var ds = btn.dataset;
    var gParams = new URLSearchParams({
      action: 'TEMPLATE',
      text: ds.calTitle || '',
      dates: toGCalDate(ds.calStart) + '/' + toGCalDate(ds.calEnd),
      details: ds.calDescription || '',
      location: ds.calLocation || ''
    });
    var oParams = new URLSearchParams({
      path: '/calendar/action/compose',
      rru: 'addevent',
      subject: ds.calTitle || '',
      body: ds.calDescription || '',
      startdt: ds.calStart || '',
      enddt: ds.calEnd || '',
      location: ds.calLocation || ''
    });
    return {
      google: 'https://calendar.google.com/calendar/render?' + gParams.toString(),
      outlook: 'https://outlook.live.com/calendar/0/deeplink/compose?' + oParams.toString(),
      office365: 'https://outlook.office.com/calendar/0/deeplink/compose?' + oParams.toString(),
      ics: ds.calIcs || ''
    };
  }

  function closeAllMenus(except) {
    var menus = document.querySelectorAll('.cal-picker-menu');
    for (var i = 0; i < menus.length; i++) {
      if (menus[i] !== except) {
        menus[i].classList.add('hidden');
        menus[i].style.top = '';
        menus[i].style.right = '';
        menus[i].style.left = '';
      }
    }
  }

  // positionMenu pins a fixed-position menu's top-right corner just
  // below the trigger's bottom-right corner. Fixed positioning lets
  // the menu escape ancestor overflow:hidden contexts (e.g. the
  // dashboard shift-table wrapper); the trade-off is the menu stays
  // in place if the user scrolls — close-on-scroll covers that.
  function positionMenu(trigger, menu) {
    var rect = trigger.getBoundingClientRect();
    menu.style.top = (rect.bottom + 4) + 'px';
    menu.style.right = (window.innerWidth - rect.right) + 'px';
    menu.style.left = 'auto';
  }

  document.addEventListener('click', function (e) {
    var trigger = e.target.closest && e.target.closest('[data-cal-trigger]');
    if (trigger) {
      e.preventDefault();
      // stopPropagation keeps the click from bubbling to a wrapping
      // <a> (e.g. the agenda session card's full-card click-through
      // anchor that takes you to the talk detail page).
      e.stopPropagation();
      var wrapper = trigger.parentElement;
      var menu = wrapper.querySelector('.cal-picker-menu');
      if (!menu) return;
      var willOpen = menu.classList.contains('hidden');
      closeAllMenus(willOpen ? menu : null);
      if (willOpen) {
        var urls = buildURLs(trigger);
        var keys = ['google', 'outlook', 'office365', 'ics'];
        for (var i = 0; i < keys.length; i++) {
          var link = menu.querySelector('[data-cal-target="' + keys[i] + '"]');
          if (link) link.href = urls[keys[i]];
        }
        positionMenu(trigger, menu);
        menu.classList.remove('hidden');
      }
      return;
    }
    // Click outside any picker — close every menu.
    if (!e.target.closest || !e.target.closest('.cal-picker')) {
      closeAllMenus(null);
    }
  });

  // Close on scroll / resize — fixed-positioned menu doesn't track
  // its trigger so leaving it open while the page moves looks broken.
  window.addEventListener('scroll', function () { closeAllMenus(null); }, true);
  window.addEventListener('resize', function () { closeAllMenus(null); });

  // ESC closes any open menu — keyboard accessibility.
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAllMenus(null);
  });
})();
