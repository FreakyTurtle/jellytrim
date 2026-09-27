// JellyTrim: the little JavaScript that HTMX and CSS cannot do.
//  1. Copy buttons for technical details (<pre> inside .callout__details).
//  2. Opening and closing <dialog> elements with data-dialog-open="id" and
//     data-dialog-close, returning focus to the trigger.
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

  // ---------- Delegated clicks ----------

  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!(target instanceof Element)) return;

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
