document.addEventListener("click", function (event) {
  var target = event.target.closest("[data-copy]");
  if (!target) return;

  var text = target.getAttribute("data-copy");
  navigator.clipboard.writeText(text).then(function () {
    var original = target.textContent;
    target.textContent = "Copied!";
    setTimeout(function () {
      target.textContent = original;
    }, 1500);
  });
});
