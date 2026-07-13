// exercise-verify.js — the exercise proof client (the platform's own code,
// served at /js/exercise-verify.js). The result page an exercise image serves
// loads this from the lessons origin; here it screenshots the rendered page
// and submits the capture to the scoring API, which perceptual-hashes it
// against the reference computed at build time. Mirrors the legacy
// training-exercises-template contract.
//
// URL params (appended by the lesson page's "Test Exercise" button):
//   hash_code     — the exercise challenge hash to grade against
//   ctfdDomain    — scoring API origin (defaults to lessonsDomain / self)
//   lessonsDomain — where to load html2canvas from
(function () {
  "use strict";
  var params = new URLSearchParams(window.location.search);
  var hash = params.get("hash_code");
  if (!hash) return; // opened without a challenge (plain preview) — do nothing.

  var clean = function (u) { return (u || "").replace(/\/+$/, ""); };
  var lessons = clean(params.get("lessonsDomain")) || window.location.origin;
  var ctfd = clean(params.get("ctfdDomain")) || lessons;

  // Small fixed status chip so the learner sees the submission happen.
  var chip = document.createElement("div");
  chip.style.cssText =
    "position:fixed;right:14px;bottom:14px;z-index:2147483647;padding:8px 12px;" +
    "border-radius:8px;font:13px/1.4 -apple-system,Segoe UI,Roboto,sans-serif;" +
    "background:#171a23;color:#e6e8ee;border:1px solid #262b38;box-shadow:0 2px 12px rgba(0,0,0,.4)";
  var setChip = function (text, color) {
    chip.textContent = text;
    chip.style.color = color || "#e6e8ee";
    if (!chip.parentNode) document.body.appendChild(chip);
  };

  function loadHtml2canvas(cb) {
    if (window.html2canvas) return cb();
    var s = document.createElement("script");
    s.src = lessons + "/assets/html2canvas.min.js";
    s.onload = cb;
    s.onerror = function () { setChip("couldn't load capture library", "#f05252"); };
    document.head.appendChild(s);
  }

  function submit() {
    setChip("capturing result…");
    loadHtml2canvas(function () {
      window
        .html2canvas(document.documentElement, {
          width: 1024,
          height: 768,
          windowWidth: 1024,
          windowHeight: 768,
          backgroundColor: null,
          logging: false,
        })
        .then(function (canvas) {
          var dataURL = canvas.toDataURL("image/jpeg", 0.85);
          setChip("submitting proof…");
          return fetch(ctfd + "/api/v1/challenges/attempt", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include", // attribute the solve when logged in
            body: JSON.stringify({ challenge_hash: hash, submission: dataURL }),
          });
        })
        .then(function (r) { return r.json(); })
        .then(function (j) {
          if (j && j.success) {
            var status = j.data && j.data.status;
            if (status === "correct") setChip("✓ proof accepted — recorded", "#31c48d");
            else setChip("✓ proof submitted (not a match yet)", "#e6e8ee");
          } else {
            setChip("submission rejected", "#f05252");
          }
        })
        .catch(function () { setChip("submission failed", "#f05252"); });
    });
  }

  // A page may ship its own trigger (#sendResult); otherwise auto-submit once
  // the page has settled, so clicking "Test Exercise" is all the learner does.
  function start() {
    var btn = document.getElementById("sendResult");
    if (btn) {
      btn.addEventListener("click", submit);
      setChip("ready — click “Send Result”");
    } else {
      setTimeout(submit, 400);
    }
  }
  if (document.readyState === "complete") start();
  else window.addEventListener("load", start);
})();
