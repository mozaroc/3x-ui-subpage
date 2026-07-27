document.addEventListener("click", function (event) {
  var target = event.target.closest("[data-copy]");
  if (!target) return;

  var text = target.getAttribute("data-copy");
  navigator.clipboard.writeText(text).then(function () {
    // A CSS overlay (.is-copied) rather than swapping textContent: some
    // data-copy buttons (the QR image) contain an <img>, and
    // textContent = "Copied!" would silently delete it.
    target.classList.add("is-copied");
    setTimeout(function () {
      target.classList.remove("is-copied");
    }, 1500);
  });
});

function detectPlatform() {
  var ua = navigator.userAgent || "";
  if (/android/i.test(ua)) return "android";
  if (/iphone|ipad|ipod/i.test(ua) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1)) return "ios";
  if (/win/i.test(ua)) return "windows";
  if (/mac/i.test(ua)) return "macos";
  if (/linux/i.test(ua)) return "linux";
  return "";
}

function applyPlatformFilter(select) {
  var value = select.value.toLowerCase();
  document.querySelectorAll(".app-card").forEach(function (card) {
    var platforms = (card.getAttribute("data-platforms") || "").toLowerCase();
    var show = !value || platforms.indexOf(value) !== -1;
    card.style.display = show ? "" : "none";
  });
}

document.addEventListener("DOMContentLoaded", function () {
  var select = document.querySelector("[data-platform-filter]");
  if (!select) return;
  var detected = detectPlatform();
  if (detected) {
    select.value = detected;
  }
  applyPlatformFilter(select);
  select.addEventListener("change", function () {
    applyPlatformFilter(select);
  });
});
