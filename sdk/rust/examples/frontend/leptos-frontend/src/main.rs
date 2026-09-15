use leptos::*;

fn main() {
    mount_to_body(|| view! { <App />})
}

#[component]
fn App() -> impl IntoView {
    let (count, set_count) = create_signal(0);

    view! {
        <div class="flex flex-col items-center gap-2">
            <div class="text-2xl font-bold select-none">"Hello from Leptos!"</div>
            <div class="flex flex-col border-2 p-2 rounded gap-2">
                <div class="font-bold text-center text-xl">{move || count.get()}</div>
                <div class="flex gap-1">
                    <button class="bg-green-600 hover:bg-green-700 text-white font-bold py-2 px-4 rounded select-none"
                        on:click=move |_| {
                            set_count.set(count.get() + 1);
                        }
                    >
                        "Increment"
                    </button>
                    <button class="bg-red-600 hover:bg-red-700 text-white font-bold py-2 px-4 rounded select-none"
                        on:click=move |_| {
                            set_count.set(count.get() - 1);
                        }
                    >
                        "Decrement"
                    </button>
                </div>
            </div>
        </div>
    }
}