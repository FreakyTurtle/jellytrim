// JellyTrim: the little JavaScript that HTMX and CSS cannot do.
//  1. Copy buttons for technical details (<pre> inside .callout__details).
//  2. Opening and closing <dialog> elements with data-dialog-open="id" and
//     data-dialog-close, returning focus to the trigger.
//  3. The schedule grid ([data-schedule]): click-and-drag painting, and one
//     tab stop with arrow keys between the hours.
// No dependencies. Event delegation, so content swapped in by HTMX works too.
(function () {
  "use strict";

  // Lets CSS show controls that only work with JavaScript.
  document.documentElement.classList.add("js");

  // ---------- Copy ----------

  function copyWithSelection(text) {
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.className = "visually-hidden";
    document.body.appendChild(area);
    area.select();
    var ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (e) {
      ok = false;
    }
    area.remove();
    return ok ? Promise.resolve() : Promise.reject(new Error("copy failed"));
  }

  function copyText(text) {
    // The Clipboard API needs a secure context. JellyTrim is often reached
    // over plain http on a home network, so fall back to a selection copy.
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).catch(function () {
        return copyWithSelection(text);
      });
    }
    return copyWithSelection(text);
  }

  function copy(button) {
    var details = button.closest(".callout__details");
    var pre = details && details.querySelector("pre");
    if (!pre) return;
    var status = details.querySelector("[data-copy-status]");
    function say(message) {
      if (!status) return;
      status.textContent = message;
      window.clearTimeout(button._copyTimer);
      button._copyTimer = window.setTimeout(function () {
        status.textContent = "";
      }, 4000);
    }
    copyText(pre.textContent)
      .then(function () {
        say("Copied");
      })
      .catch(function () {
        say("Could not copy. Select the text and copy it by hand.");
      })
      .then(function () {
        button.focus();
      });
  }

  // ---------- Dialogs ----------

  var triggers = new WeakMap();

  function openDialog(trigger) {
    var dialog = document.getElementById(trigger.getAttribute("data-dialog-open"));
    if (!dialog || typeof dialog.showModal !== "function") return false;
    if (dialog.open) return true;
    triggers.set(dialog, trigger);
    dialog.showModal();
    // Focus the safe choice unless the markup chose something with autofocus.
    if (!dialog.querySelector("[autofocus]")) {
      var safe = dialog.querySelector("[data-dialog-close]");
      if (safe) safe.focus();
    }
    return true;
  }

  // "close" does not bubble, so listen in the capture phase. This also covers
  // Escape and <form method="dialog">.
  document.addEventListener(
    "close",
    function (event) {
      var dialog = event.target;
      if (!(dialog instanceof HTMLDialogElement)) return;
      var trigger = triggers.get(dialog);
      triggers.delete(dialog);
      if (trigger && trigger.isConnected) trigger.focus();
    },
    true
  );

  // ---------- Schedule painting ----------

  // Pressing on a cell flips it, and dragging with the button held sets
  // every cell the pointer crosses to the same value. Touch is left alone,
  // so a finger can still scroll the page; a tap toggles one cell as a
  // normal checkbox does. The keyboard uses the checkboxes directly.
  var paint = null;
  var swallowClick = false;

  function scheduleInput(target) {
    if (!(target instanceof Element)) return null;
    var cell = target.closest("[data-schedule] .schedule__cell");
    return cell ? cell.querySelector("input[type=checkbox]") : null;
  }

  document.addEventListener("pointerdown", function (event) {
    if (event.pointerType === "touch" || event.button !== 0) return;
    var input = scheduleInput(event.target);
    if (!input || input.disabled) return;
    // Stop text selection and the label's own toggle on click.
    event.preventDefault();
    swallowClick = true;
    paint = { value: !input.checked, last: input };
    input.checked = paint.value;
    input.focus({ preventScroll: true });
  });

  document.addEventListener("pointermove", function (event) {
    if (!paint) return;
    if (!(event.buttons & 1)) {
      paint = null;
      return;
    }
    var input = scheduleInput(document.elementFromPoint(event.clientX, event.clientY));
    if (!input || input === paint.last || input.disabled) return;
    paint.last = input;
    input.checked = paint.value;
  });

  function endPaint() {
    paint = null;
    // The click that follows this pointerup has already been handled.
    window.setTimeout(function () {
      swallowClick = false;
    }, 0);
  }

  document.addEventListener("pointerup", endPaint);
  document.addEventListener("pointercancel", endPaint);

  // One tab stop for the whole grid, so Tab moves past 168 hours in one
  // press; the arrow keys move between hours, Home and End to the ends of a
  // day. The cells stay checkboxes, so Space still toggles and screen
  // reader browse modes still read every hour.
  function scheduleSetStop(grid, input) {
    grid.querySelectorAll("input[type=checkbox]").forEach(function (el) {
      el.tabIndex = el === input ? 0 : -1;
    });
  }

  function scheduleInit(root) {
    root.querySelectorAll("[data-schedule]").forEach(function (grid) {
      var first = grid.querySelector("input[type=checkbox]");
      if (first) scheduleSetStop(grid, first);
    });
  }

  scheduleInit(document);
  document.addEventListener("htmx:load", function (event) {
    if (event.target instanceof Element) scheduleInit(event.target);
  });

  document.addEventListener("focusin", function (event) {
    var grid = event.target instanceof Element && event.target.closest("[data-schedule]");
    if (grid && event.target.matches("input[type=checkbox]")) scheduleSetStop(grid, event.target);
  });

  var scheduleKeys = { ArrowLeft: [0, -1], ArrowRight: [0, 1], ArrowUp: [-1, 0], ArrowDown: [1, 0] };

  document.addEventListener("keydown", function (event) {
    var input = event.target;
    if (!(input instanceof HTMLInputElement) || !input.closest("[data-schedule]")) return;
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    var grid = input.closest("[data-schedule]");
    var parts = input.value.split("-");
    var day = +parts[0];
    var hour = +parts[1];
    if (event.key === "Home") {
      hour = 0;
    } else if (event.key === "End") {
      hour = 23;
    } else if (scheduleKeys[event.key]) {
      // The grid turns on its side in a narrow panel: find which way the
      // hours run from where the next hour is drawn.
      var here = input.closest("label").getBoundingClientRect();
      var next = grid.querySelector('input[value="' + day + "-" + (hour === 23 ? 22 : hour + 1) + '"]');
      var there = next.closest("label").getBoundingClientRect();
      var across = Math.abs(there.top - here.top) < 1;
      var step = scheduleKeys[event.key];
      var dRow = step[0];
      var dCol = step[1];
      if (across) {
        day += dRow;
        hour += dCol;
      } else {
        day += dCol;
        hour += dRow;
      }
    } else {
      return;
    }
    var target = grid.querySelector('input[value="' + day + "-" + hour + '"]');
    event.preventDefault();
    if (target) target.focus();
  });

  // ---------- Delegated clicks ----------

  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!(target instanceof Element)) return;

    // A pointer press on a schedule cell has already set it (see above).
    // Keyboard clicks (detail 0) are never swallowed.
    if (swallowClick && event.detail > 0 && target.closest("[data-schedule]")) {
      event.preventDefault();
      return;
    }

    var copyButton = target.closest("[data-copy]");
    if (copyButton) {
      event.preventDefault();
      copy(copyButton);
      return;
    }

    var opener = target.closest("[data-dialog-open]");
    if (opener) {
      // A trigger may be a link to a no-JavaScript fallback page.
      if (openDialog(opener)) event.preventDefault();
      return;
    }

    var closer = target.closest("[data-dialog-close]");
    if (closer) {
      var dialog = closer.closest("dialog");
      if (dialog) {
        event.preventDefault();
        dialog.close();
      }
    }
  });
})();
