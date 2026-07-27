// AI-Provenance:
//   model: claude-opus-4-6-20260115
//   harness: Cursor
//
// Progressive enhancement for the Torio docs. No dependencies, no build step.
// Both features here are additive: with JavaScript off the pages still read,
// navigate, and select code normally.
(function () {
  "use strict";

  var root = document.documentElement;

  // ---- Colour theme -------------------------------------------------------
  // Three states, cycled by the toggle: follow the OS, force light, force dark.
  var STORAGE_KEY = "torio-theme";

  function storedTheme() {
    try {
      var value = localStorage.getItem(STORAGE_KEY);
      return value === "light" || value === "dark" ? value : "system";
    } catch (e) {
      return "system";
    }
  }

  function applyTheme(theme) {
    if (theme === "system") {
      delete root.dataset.theme;
    } else {
      root.dataset.theme = theme;
    }
    try {
      if (theme === "system") {
        localStorage.removeItem(STORAGE_KEY);
      } else {
        localStorage.setItem(STORAGE_KEY, theme);
      }
    } catch (e) {}
  }

  var toggle = document.querySelector(".theme-toggle");
  if (toggle) {
    var order = ["system", "light", "dark"];
    var label = {
      system: "Theme: match system",
      light: "Theme: light",
      dark: "Theme: dark",
    };

    var describe = function (theme) {
      toggle.dataset.mode = theme;
      toggle.setAttribute("title", label[theme]);
      toggle.setAttribute("aria-label", label[theme] + ". Click to change.");
    };

    describe(storedTheme());
    toggle.addEventListener("click", function () {
      var next = order[(order.indexOf(storedTheme()) + 1) % order.length];
      applyTheme(next);
      describe(next);
    });
  }

  // ---- Copy buttons -------------------------------------------------------
  // These blocks are meant to be run verbatim, and hand-selecting a wrapped
  // multi-line command is exactly where a reader drops half of it.

  // The async clipboard API needs both a secure context and a focused
  // document, so fall back to a selection copy rather than telling a reader
  // their click failed.
  function legacyCopy(text) {
    var field = document.createElement("textarea");
    field.value = text;
    field.setAttribute("readonly", "");
    field.style.position = "fixed";
    field.style.top = "-1000px";
    field.style.opacity = "0";
    document.body.appendChild(field);
    field.select();
    var ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (e) {
      ok = false;
    }
    document.body.removeChild(field);
    return ok;
  }

  function copyText(text) {
    if (navigator.clipboard && document.hasFocus()) {
      return navigator.clipboard.writeText(text).catch(function () {
        if (!legacyCopy(text)) throw new Error("copy failed");
      });
    }
    return legacyCopy(text) ? Promise.resolve() : Promise.reject(new Error("copy failed"));
  }

  if (document.queryCommandSupported || navigator.clipboard) {
    Array.prototype.forEach.call(
      document.querySelectorAll(".code-block"),
      function (block) {
        var button = document.createElement("button");
        button.type = "button";
        button.className = "copy-button";
        button.textContent = "Copy";
        button.setAttribute("aria-label", "Copy code to clipboard");

        button.addEventListener("click", function () {
          var code = block.querySelector("code");
          var text = (code ? code.textContent : block.textContent).replace(/\s+$/, "");

          copyText(text).then(
            function () {
              button.textContent = "Copied";
              button.classList.add("is-done");
              window.setTimeout(function () {
                button.textContent = "Copy";
                button.classList.remove("is-done");
              }, 1600);
            },
            function () {
              button.textContent = "Copy failed";
              window.setTimeout(function () {
                button.textContent = "Copy";
              }, 1600);
            }
          );
        });

        block.appendChild(button);
      }
    );
  }
})();
