/* Shared mobile nav toggle for QSDM product pages. */
(function () {
  "use strict";
  var toggle = document.getElementById("siteMenuToggle");
  var mobile = document.getElementById("siteMobileNav");
  if (!toggle || !mobile) return;
  toggle.addEventListener("click", function () {
    var open = mobile.classList.toggle("is-open");
    toggle.setAttribute("aria-expanded", String(open));
  });
  mobile.querySelectorAll("a").forEach(function (a) {
    a.addEventListener("click", function () {
      mobile.classList.remove("is-open");
      toggle.setAttribute("aria-expanded", "false");
    });
  });
})();
