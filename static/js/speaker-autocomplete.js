// Speaker autocomplete for the admin "Invite a Speaker" form.
//
// Watches the visible Email input (#SpeakerEmail) for typing; when the
// query matches an existing speaker by email or name, surfaces the
// match below the input. Picking a hit autofills #SpeakerName and
// writes the speaker's page ID to a hidden #SpeakerID — the POST
// handler uses that to skip the create-new-speaker branch and link
// directly to the existing row.
//
// Typing fresh (without picking) clears #SpeakerID, so a new speaker
// is created on submit. Mirrors org-autocomplete.js shape; backed by
// /api/speakers/search.
(function () {
  const emailInput = document.getElementById('SpeakerEmail');
  const nameInput = document.getElementById('SpeakerName');
  const hidden = document.getElementById('SpeakerID');
  const flash = document.getElementById('SpeakerLookupFlash');
  if (!emailInput || !nameInput || !hidden) return;

  const wrap = emailInput.parentElement;
  if (!wrap) return;
  wrap.style.position = 'relative';

  const list = document.createElement('ul');
  list.id = 'SpeakerSuggest';
  list.className = 'absolute z-10 left-0 right-0 mt-1 max-h-60 overflow-auto rounded-md border border-gray-200 bg-white shadow hidden';
  list.setAttribute('role', 'listbox');
  wrap.appendChild(list);

  let debounceTimer = null;
  let lastQuery = '';
  let activeFetch = 0;

  function hideList() {
    list.classList.add('hidden');
    list.replaceChildren();
  }

  function showFlash(msg) {
    if (!flash) return;
    flash.textContent = msg || '';
    flash.classList.toggle('hidden', !msg);
  }

  function renderHits(hits) {
    list.replaceChildren();
    if (!hits || hits.length === 0) {
      list.classList.add('hidden');
      return;
    }
    for (const hit of hits) {
      const li = document.createElement('li');
      li.className = 'px-3 py-2 text-sm hover:bg-indigo-50 cursor-pointer';
      li.setAttribute('role', 'option');
      li.dataset.id = hit.id;
      li.dataset.name = hit.name;
      li.dataset.email = hit.email;

      const top = document.createElement('div');
      top.className = 'font-medium text-gray-900';
      top.textContent = hit.name || hit.email;
      li.appendChild(top);

      const sub = document.createElement('div');
      sub.className = 'text-xs text-gray-500';
      sub.textContent = hit.email + (hit.company ? ' · ' + hit.company : '');
      li.appendChild(sub);

      li.addEventListener('mousedown', function (ev) {
        ev.preventDefault();
        emailInput.value = hit.email;
        nameInput.value = hit.name;
        hidden.value = hit.id;
        showFlash('Reusing existing speaker: ' + (hit.name || hit.email) + '.');
        hideList();
      });
      list.appendChild(li);
    }
    list.classList.remove('hidden');
  }

  function search(q) {
    const fetchID = ++activeFetch;
    fetch('/api/speakers/search?q=' + encodeURIComponent(q), { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (hits) {
        if (fetchID !== activeFetch) return;
        renderHits(hits);
      })
      .catch(function () { /* silent */ });
  }

  emailInput.addEventListener('input', function () {
    hidden.value = '';
    showFlash('');
    const q = emailInput.value.trim();
    if (q === lastQuery) return;
    lastQuery = q;
    if (debounceTimer) clearTimeout(debounceTimer);
    if (q.length < 2) {
      hideList();
      return;
    }
    debounceTimer = setTimeout(function () { search(q); }, 200);
  });

  emailInput.addEventListener('blur', function () {
    setTimeout(hideList, 150);
  });

  emailInput.setAttribute('autocomplete', 'off');
  emailInput.setAttribute('role', 'combobox');
  emailInput.setAttribute('aria-autocomplete', 'list');
  emailInput.setAttribute('aria-controls', 'SpeakerSuggest');
})();
