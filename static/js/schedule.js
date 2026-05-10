// Schedule drag-and-drop. HTML5 native drag API; no framework.
//
// Optimistic UI: when a card gets dropped, we move it in the DOM
// immediately (so it appears in place instantly), then POST to the
// server in the background. On a server error we revert the move and
// surface the error. The page no longer reloads after every drop.
(function () {
  "use strict";

  const root = document.querySelector("[data-conf-tag]");
  if (!root) return;
  const confTag = root.dataset.confTag;
  const pxPerMin = parseInt(root.dataset.pxPerMin || "2", 10);
  const snapMin = parseInt(root.dataset.snapMin || "15", 10);

  const sidebar = document.getElementById("schedule-sidebar");
  const sidebarList = sidebar ? sidebar.querySelector(".space-y-2") : null;

  // CSS classes that differ between a sidebar card and a placed card.
  // We swap the differing ones during morphs and leave shared ones
  // (.schedule-talk, .cursor-grab, padding, etc.) untouched. Color
  // classes are split out so they can be toggled independently
  // based on the post-mutation drift state.
  const sidebarOnlyClasses = ["border-gray-200", "bg-white"];
  const placedLayoutClasses = [
    "absolute",
    "left-1",
    "right-1",
    "overflow-hidden",
    "group",
  ];
  const placedCleanClasses = ["border-indigo-200", "bg-indigo-50/80"];
  const placedDriftClasses = ["border-amber-400", "bg-amber-50/90"];

  let dragging = null;

  function attachDragHandlers(card) {
    card.addEventListener("dragstart", (ev) => {
      if (card.dataset.resizing === "1") {
        ev.preventDefault();
        return;
      }
      dragging = card;
      card.classList.add("dragging");
      ev.dataTransfer.effectAllowed = "move";
      ev.dataTransfer.setData("text/plain", card.dataset.proposalId);
    });
    card.addEventListener("dragend", () => {
      card.classList.remove("dragging");
      dragging = null;
    });
  }

  function attachResizeHandle(handle) {
    handle.addEventListener("mousedown", (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      const card = handle.closest(".schedule-talk");
      if (!card) return;

      card.dataset.resizing = "1";
      card.draggable = false;

      const startY = ev.clientY;
      const startMin = parseInt(card.dataset.actualMin || "30", 10);
      const startHeight = card.offsetHeight;
      const proposalID = card.dataset.proposalId;
      const minDuration = snapMin;

      const onMove = (mv) => {
        const dy = mv.clientY - startY;
        let newDur = startMin + Math.round(dy / pxPerMin);
        newDur = Math.round(newDur / snapMin) * snapMin;
        if (newDur < minDuration) newDur = minDuration;
        card.style.height = newDur * pxPerMin + "px";
        const label = card.querySelector(".schedule-actual-label");
        if (label) label.textContent = newDur;
        card.dataset.pendingMin = newDur;
      };

      const onUp = async () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);

        const finalDur = parseInt(card.dataset.pendingMin || startMin, 10);
        delete card.dataset.resizing;
        card.draggable = true;

        if (finalDur === startMin) return;
        try {
          const resp = await fetch(`/${confTag}/admin/schedule/resize`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ proposalID, durationMin: finalDur }),
          });
          if (!resp.ok) {
            const text = await resp.text();
            alert("Couldn't resize: " + (text || resp.status));
            card.style.height = startHeight + "px";
            const label = card.querySelector(".schedule-actual-label");
            if (label) label.textContent = startMin;
            return;
          }
          card.dataset.actualMin = finalDur;
          const data = await resp.json().catch(() => ({}));
          applyDriftState(card, !!data.hasDrift);
        } catch (err) {
          alert("Network error: " + err.message);
          card.style.height = startHeight + "px";
        }
      };

      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    });
  }

  // morphToPlaced rewrites a card's classes / styles so it renders as
  // an absolute-positioned block inside a venue column. Ensures a
  // resize handle exists. Idempotent — calling on an already-placed
  // card just updates the position. Color classes (drift vs clean)
  // are managed separately by applyDriftState after the network
  // response lands.
  function morphToPlaced(card, venueEl, topPx) {
    sidebarOnlyClasses.forEach((c) => card.classList.remove(c));
    placedLayoutClasses.forEach((c) => card.classList.add(c));
    // Default to the current visual state so the card doesn't
    // flicker between drag and response. The post-response
    // applyDriftState picks the truthy color.
    if (!card.classList.contains("border-amber-400") &&
        !card.classList.contains("border-indigo-200")) {
      placedCleanClasses.forEach((c) => card.classList.add(c));
    }
    card.style.minHeight = "";
    card.style.top = topPx + "px";
    if (!card.style.height) {
      const dur = parseInt(card.dataset.actualMin || card.dataset.desiredMin || "30", 10);
      card.style.height = dur * pxPerMin + "px";
    }
    if (card.parentNode !== venueEl) {
      venueEl.appendChild(card);
    }
    if (!card.querySelector(".schedule-resize-handle")) {
      const handle = document.createElement("div");
      handle.className =
        "schedule-resize-handle absolute inset-x-0 bottom-0 h-1.5 cursor-ns-resize bg-indigo-300/0 hover:bg-indigo-400/60 transition-colors";
      handle.title = "Drag to resize";
      card.appendChild(handle);
      attachResizeHandle(handle);
    }
  }

  // morphToSidebar reverses morphToPlaced — used when a placed card is
  // dragged back to the sidebar list.
  function morphToSidebar(card) {
    placedLayoutClasses.forEach((c) => card.classList.remove(c));
    placedCleanClasses.forEach((c) => card.classList.remove(c));
    placedDriftClasses.forEach((c) => card.classList.remove(c));
    sidebarOnlyClasses.forEach((c) => card.classList.add(c));
    // Drop the EDITED pill when going back to the sidebar; the
    // talk is unscheduled, no drift signal makes sense.
    removeDriftPill(card);
    card.style.top = "";
    card.style.height = "";
    const dur = parseInt(card.dataset.desiredMin || "30", 10);
    card.style.minHeight = dur * pxPerMin + "px";
    const handle = card.querySelector(".schedule-resize-handle");
    if (handle) handle.remove();
    delete card.dataset.conftalkId;
    if (sidebarList) sidebarList.appendChild(card);
  }

  // applyDriftState swaps the color classes on a placed card and
  // shows / hides the EDITED pill based on the server-reported
  // drift state. Called after every successful place / resize so
  // the orange tint stays in sync with what the schedule UI shows
  // versus what's been sent to attendees' calendars.
  function applyDriftState(card, hasDrift) {
    if (hasDrift) {
      placedCleanClasses.forEach((c) => card.classList.remove(c));
      placedDriftClasses.forEach((c) => card.classList.add(c));
      ensureDriftPill(card);
    } else {
      placedDriftClasses.forEach((c) => card.classList.remove(c));
      placedCleanClasses.forEach((c) => card.classList.add(c));
      removeDriftPill(card);
    }
  }

  // ensureDriftPill injects the "EDITED" badge into a placed card
  // when it isn't already there. Matches the server-rendered
  // markup in schedule_placed so refreshes look identical to
  // post-drag state.
  function ensureDriftPill(card) {
    if (card.querySelector(".schedule-drift-pill")) return;
    // The pill belongs next to the status pill in the card's
    // top-right action group. Schema:
    //   <div class="flex items-start justify-between gap-1">
    //     <p>title</p>
    //     <div class="flex items-center gap-1 shrink-0">
    //       [EDITED?] [STATUS]
    //     </div>
    //   </div>
    const actionGroup = card.querySelector(".flex.items-start.justify-between > div.flex");
    if (!actionGroup) return;
    const pill = document.createElement("span");
    pill.title = "Edited since the last cal invite was sent — needs a Send Cal Updates click.";
    pill.className = "schedule-drift-pill inline-flex items-center px-1.5 py-0.5 rounded text-[9px] font-bold bg-amber-200 text-amber-900";
    pill.textContent = "EDITED";
    actionGroup.insertBefore(pill, actionGroup.firstChild);
  }

  function removeDriftPill(card) {
    const pill = card.querySelector(".schedule-drift-pill");
    if (pill) pill.remove();
  }

  // Snapshot of a card's position before a drag. Used to revert on
  // server error so the user doesn't end up with the UI claiming a
  // placement that didn't actually persist.
  function snapshot(card) {
    return {
      parent: card.parentNode,
      nextSibling: card.nextSibling,
      classes: card.className,
      styleTop: card.style.top,
      styleHeight: card.style.height,
      styleMinHeight: card.style.minHeight,
      hadHandle: !!card.querySelector(".schedule-resize-handle"),
      datasetActual: card.dataset.actualMin,
      datasetConfTalkID: card.dataset.conftalkId,
    };
  }

  function restore(card, snap) {
    if (snap.parent && snap.nextSibling) {
      snap.parent.insertBefore(card, snap.nextSibling);
    } else if (snap.parent) {
      snap.parent.appendChild(card);
    }
    card.className = snap.classes;
    card.style.top = snap.styleTop;
    card.style.height = snap.styleHeight;
    card.style.minHeight = snap.styleMinHeight;
    const handle = card.querySelector(".schedule-resize-handle");
    if (handle && !snap.hadHandle) handle.remove();
    if (snap.datasetActual !== undefined) card.dataset.actualMin = snap.datasetActual;
    if (snap.datasetConfTalkID === undefined) {
      delete card.dataset.conftalkId;
    } else {
      card.dataset.conftalkId = snap.datasetConfTalkID;
    }
  }

  // Wire every existing card.
  document.querySelectorAll(".schedule-talk").forEach(attachDragHandlers);
  document.querySelectorAll(".schedule-resize-handle").forEach(attachResizeHandle);

  // Each venue column accepts drops.
  document.querySelectorAll(".schedule-venue").forEach((venueEl) => {
    venueEl.addEventListener("dragover", (ev) => {
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "move";
      venueEl.classList.add("over");
    });
    venueEl.addEventListener("dragleave", () => {
      venueEl.classList.remove("over");
    });
    venueEl.addEventListener("drop", async (ev) => {
      ev.preventDefault();
      venueEl.classList.remove("over");
      const proposalID = ev.dataTransfer.getData("text/plain");
      if (!proposalID) return;
      const card =
        dragging ||
        document.querySelector(`.schedule-talk[data-proposal-id="${CSS.escape(proposalID)}"]`);
      if (!card) return;

      const opensMin = parseInt(venueEl.dataset.opensMin, 10);
      const closesMin = parseInt(venueEl.dataset.closesMin, 10);
      const rect = venueEl.getBoundingClientRect();
      const offsetY = ev.clientY - rect.top;

      let minOfDay = opensMin + Math.round(offsetY / pxPerMin);
      minOfDay = Math.round(minOfDay / snapMin) * snapMin;
      if (minOfDay < opensMin) minOfDay = opensMin;
      if (minOfDay > closesMin - snapMin) minOfDay = closesMin - snapMin;

      const day = parseInt(venueEl.dataset.day, 10);
      const venue = venueEl.dataset.venue;
      const topPx = (minOfDay - opensMin) * pxPerMin;

      // Optimistic: snapshot, then move the card to its new spot
      // before the network call. On error we restore from snapshot.
      const snap = snapshot(card);
      morphToPlaced(card, venueEl, topPx);

      try {
        const resp = await fetch(`/${confTag}/admin/schedule/place`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            proposalID,
            day,
            venue,
            startMin: minOfDay,
          }),
        });
        if (!resp.ok) {
          const text = await resp.text();
          restore(card, snap);
          alert("Couldn't place: " + (text || resp.status));
          return;
        }
        const data = await resp.json().catch(() => ({}));
        if (data.confTalkID) card.dataset.conftalkId = data.confTalkID;
        applyDriftState(card, !!data.hasDrift);
      } catch (err) {
        restore(card, snap);
        alert("Network error: " + err.message);
      }
    });
  });

  // Sidebar drop = un-schedule. Optimistic too.
  if (sidebar) {
    sidebar.addEventListener("dragover", (ev) => {
      if (!dragging || !dragging.closest(".schedule-venue")) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "move";
      sidebar.classList.add("over");
    });
    sidebar.addEventListener("dragleave", () => {
      sidebar.classList.remove("over");
    });
    sidebar.addEventListener("drop", async (ev) => {
      sidebar.classList.remove("over");
      if (!dragging || !dragging.closest(".schedule-venue")) return;
      ev.preventDefault();
      const card = dragging;
      const proposalID = card.dataset.proposalId;
      if (!proposalID) return;

      const snap = snapshot(card);
      morphToSidebar(card);

      try {
        const resp = await fetch(`/${confTag}/admin/schedule/unplace`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ proposalID }),
        });
        if (!resp.ok) {
          const text = await resp.text();
          restore(card, snap);
          alert("Couldn't unschedule: " + (text || resp.status));
        }
      } catch (err) {
        restore(card, snap);
        alert("Network error: " + err.message);
      }
    });
  }
})();
