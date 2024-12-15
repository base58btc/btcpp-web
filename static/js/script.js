function closeMenu(el) {
	document.querySelectorAll('[role="nav-dialog"]').forEach(function (el){
		el.classList.add("hidden");
	})
}

function toggleMenu(el) {
	document.querySelectorAll('[role="nav-dialog"]').forEach(function (el){
		if (el.classList.contains("hidden")) {
			el.classList.remove("hidden");
		} else {
			el.classList.add("hidden");
		}
	});

	return true;
}

function toggleMobileFlyout(el, select) {
	document.querySelectorAll('[role="mobile-flyout-' + select + '"]').forEach(function (el){
		if (el.classList.contains("hidden")) {
			el.classList.remove("hidden");
		} else {
			el.classList.add("hidden");
		}
	});
	document.querySelectorAll('[role="nav-caret-' + select + '"]').forEach(function (el){
		if (el.classList.contains("rotate-180")) {
			el.classList.remove("rotate-180");
		} else {
			el.classList.add("rotate-180");
		}
	});

	return true;
}

function toggleNavFlyout(el) {
	document.querySelectorAll('[role="nav-flyout"]').forEach(function (el){
		if (el.classList.contains("transition-in")) {
			el.classList.remove("transition-in");
			el.classList.add("transition-out");
			el.style.transform = "translateY(-100%)";
			el.style.opacity = 0;
		} else {
			el.classList.remove("transition-out");
			el.classList.add("transition-in");
			el.style.transform = "translateY(0%)";
			el.style.opacity = 1;
		}
	});

	return true;
}

function togglePayment(type) {
  // Update toggle buttons
  document.querySelectorAll('.toggle-btn').forEach(btn => {
    btn.classList.remove('active');
  });
  event.target.classList.add('active');

  // Show/hide payment options
  document.querySelectorAll('.payment-option').forEach(option => {
    option.classList.remove('active');
  });
  document.getElementById(type + '-option').classList.add('active');
}
