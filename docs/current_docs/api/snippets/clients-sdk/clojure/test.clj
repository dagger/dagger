(ns my-app.test
  (:gen-class)
  (:import
   (io.dagger.client Dagger)))

(defn -main
  []
  (with-open [dag (Dagger/connect)]
    (let [src (-> dag .host (.directory "."))]
      (for [v '(17 21 23)]
        (println
         (-> dag
             .container
             (.from (str "clojure:temurin-" v "-lein"))
             (.withDirectory "/src" src)
             (.withWorkdir "/src")
             (.withExec '("lein" "test"))
             .stdout)
         )
        ))))
