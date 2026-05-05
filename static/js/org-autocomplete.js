// Org autocomplete for the speaker-info editor.
//
// Wraps a normal <input> with a server-backed typeahead. Picking a
// suggestion fills the visible Company field and writes the picked org's
// page ID into a hidden #OrgID input so the form submission links to it.
// Typing free-form (no pick) clears the OrgID — the SpeakerConf will
// keep just the typed Company text without linking to an Org row.
(function () {
  const input = document.getElementById('Company');
  const hidden = document.getElementById('OrgID');
  if (!input || !hidden) return;

  const wrap = input.parentElement;
  if (!wrap) return;
  wrap.style.position = 'relative';

  const list = document.createElement('ul');
  list.id = 'OrgSuggest';
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
      li.textContent = hit.name;
      if (hit.website) {
        const sub = document.createElement('span');
        sub.className = 'ml-2 text-xs text-gray-400';
        sub.textContent = hit.website;
        li.appendChild(sub);
      }
      li.addEventListener('mousedown', function (ev) {
        // mousedown so it fires before input's blur
        ev.preventDefault();
        input.value = hit.name;
        hidden.value = hit.id;
        hideList();
      });
      list.appendChild(li);
    }
    list.classList.remove('hidden');
  }

  function search(q) {
    const fetchID = ++activeFetch;
    fetch('/api/orgs/search?q=' + encodeURIComponent(q), { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (hits) {
        // Drop stale responses
        if (fetchID !== activeFetch) return;
        renderHits(hits);
      })
      .catch(function () { /* network errors → silently no suggestions */ });
  }

  input.addEventListener('input', function () {
    // User is typing fresh — any previously-picked org no longer reflects
    // the visible name, so unlink.
    hidden.value = '';
    const q = input.value.trim();
    if (q === lastQuery) return;
    lastQuery = q;
    if (debounceTimer) clearTimeout(debounceTimer);
    if (q.length < 2) {
      hideList();
      return;
    }
    debounceTimer = setTimeout(function () { search(q); }, 200);
  });

  input.addEventListener('blur', function () {
    // Slight delay so click-to-select fires first
    setTimeout(hideList, 150);
  });

  input.setAttribute('autocomplete', 'off');
  input.setAttribute('role', 'combobox');
  input.setAttribute('aria-autocomplete', 'list');
  input.setAttribute('aria-controls', 'OrgSuggest');
})();
