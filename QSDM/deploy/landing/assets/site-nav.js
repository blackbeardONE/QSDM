/* Shared mobile nav + dropdowns + active-link marking for QSDM pages. */
(function () {
  "use strict";

  var toggle = document.getElementById("siteMenuToggle");
  var mobile = document.getElementById("siteMobileNav");
  if (toggle && mobile) {
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
  }

  var path = (window.location.pathname || "/").replace(/\/+$/, "") || "/";
  var pathFile = path.split("/").pop() || "";
  var isHome = path === "/" || pathFile === "" || pathFile === "index.html";
  var isDocs = path.indexOf("/docs") === 0;

  function matches(href) {
    if (!href || href.indexOf("http") === 0 || href.charAt(0) === "#") return false;
    var clean = href.replace(/\/+$/, "") || "/";
    if (clean === "/" || clean === "/index.html") return isHome;
    if (clean === "/docs" || clean.indexOf("/docs/") === 0) return isDocs;
    return path === clean || path.endsWith(clean) || pathFile === clean.replace(/^\//, "");
  }

  document.querySelectorAll(".site-nav a, .site-mobile-nav a").forEach(function (a) {
    if (matches(a.getAttribute("href") || "")) {
      a.classList.add("active");
      a.setAttribute("aria-current", "page");
    }
  });

  document.querySelectorAll(".site-nav-item").forEach(function (item) {
    if (item.querySelector("a.active, a[aria-current='page']")) {
      item.classList.add("is-active");
    }
  });

  var items = Array.prototype.slice.call(document.querySelectorAll(".site-nav-item"));
  items.forEach(function (item) {
    var trigger = item.querySelector(".site-nav-trigger");
    if (!trigger) return;

    trigger.addEventListener("click", function (event) {
      event.preventDefault();
      var open = !item.classList.contains("is-open");
      items.forEach(function (other) {
        other.classList.remove("is-open");
        var otherTrigger = other.querySelector(".site-nav-trigger");
        if (otherTrigger) otherTrigger.setAttribute("aria-expanded", "false");
      });
      if (open) {
        item.classList.add("is-open");
        trigger.setAttribute("aria-expanded", "true");
      }
    });
  });

  document.addEventListener("click", function (event) {
    if (event.target.closest(".site-nav-item")) return;
    items.forEach(function (item) {
      item.classList.remove("is-open");
      var trigger = item.querySelector(".site-nav-trigger");
      if (trigger) trigger.setAttribute("aria-expanded", "false");
    });
  });

  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") return;
    items.forEach(function (item) {
      item.classList.remove("is-open");
      var trigger = item.querySelector(".site-nav-trigger");
      if (trigger) trigger.setAttribute("aria-expanded", "false");
    });
  });
})();
