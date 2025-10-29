function loadAndHighlightCodeSnippets() {
    Array.prototype.slice.call(document.querySelectorAll('pre[data-src]')).forEach(function (pre) {
        var src = pre.getAttribute('data-src').replace(/\\/g, '/');
        var language = 'php'; // Puoi migliorarlo per rilevare il linguaggio dinamicamente

        var code = document.createElement('code');
        code.className = 'language-' + language;

        pre.textContent = ''; // Pulisce il contenuto del <pre>

        code.textContent = 'Loading…'; // Messaggio di caricamento
        pre.appendChild(code);

        var xhr = new XMLHttpRequest();
        xhr.open('GET', src, true);

        xhr.onreadystatechange = function () {
            if (xhr.readyState === 4) {
                if (xhr.status < 400 && xhr.responseText) {
                    code.textContent = xhr.responseText;
                    Prism.highlightElement(code); // Evidenzia con Prism
                } else if (xhr.status >= 400) {
                    code.textContent = '✖ Error ' + xhr.status + ' while fetching file: ' + xhr.statusText;
                } else {
                    code.textContent = '✖ Error: File does not exist, is empty or trying to view from localhost';
                }
            }
        };

        xhr.send(null);
    });
}

// Assicurati che il documento sia pronto e poi carica i frammenti di codice
$(document).ready(function () {
    loadAndHighlightCodeSnippets();
});

// Attivazione anche quando il contenuto di 'source-view' è mostrato
$('#source-view').on('shown', function () {
    loadAndHighlightCodeSnippets();
});
