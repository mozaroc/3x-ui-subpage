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
