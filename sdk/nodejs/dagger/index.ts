import axios, { AxiosInstance } from 'axios';
import * as fs from 'fs';

type ActionCallback = (inputs: any) => any;

class Dagger {
    private client: AxiosInstance

    constructor() {
        this.client = axios.create({
            // baseURL: "http://localhost",
            socketPath: "/dagger.sock",
            timeout: 15e3,
        });
    };

    public async do(payload: string): Promise<any> {
        const response = await this.client({
            method: 'get',
            url: `http://localhost/graphql?payload=${encodeURI(payload)}`,
        })
        return response;
    }

    action(name: string, callback: ActionCallback): void {
        if (name != process.env.DAGGER_ACTION) {
            return
        }

        const inputs = JSON.parse(fs.readFileSync('/inputs/dagger.json', 'utf8'));

        const outputs = callback(inputs);

        fs.writeFileSync("/outputs/dagger.json", JSON.stringify(outputs));
    }
}

export default Dagger;