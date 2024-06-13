  (function() {
    if (typeof window === 'undefined') return;
  if (typeof window.signals !== 'undefined') return;
  var script = document.createElement('script');
  script.src = 'https://cdn.cr-relay.com/v1/site/61470647-07da-41d0-892f-308f7e3345f1/signals.js';
  script.async = true;
  window.signals = Object.assign(
    [],
    ['page', 'identify', 'form'].reduce(function (acc, method){
      acc[method] = function () {
        signals.push([method, arguments]);
        return signals;
      };
      return acc;
    }, {})
  );
  document.head.appendChild(script);
  })();
