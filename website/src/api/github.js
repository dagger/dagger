import axios from 'axios';

const AxiosInstance = axios.create({
    headers: { 'Accept': 'application/vnd.github.v3+json' },
});

async function getAccessToken(code) {

    try {
        const getAccessToken = await AxiosInstance.get('https://github.com/login/oauth/access_token', {
            params: {
                code,
                client_id: process.env.REACT_APP_CLIENT_ID,
                client_secret: process.env.REACT_APP_CLIENT_SECRET,
            },
            validateStatus: function (status) {
                return status < 500; // Resolve only if the status code is less than 500
            }
        })

        return getAccessToken.data;
    } catch (error) {
        console.log("error getAccessToken", error.message)
    }
}

export async function getUser(access_token) {
    try {
        const getUserLogin = await AxiosInstance.get("https://api.github.com/user", {
            headers: { Authorization: `token ${access_token}` },
            validateStatus: function (status) {
                return status < 500; // Resolve only if the status code is less than 500
            }
        })

        return {
            login: getUserLogin.data.login,
            status: getUserLogin.status
        }
    } catch (error) {
        console.log("error getUser", error.message)
    }
}

export async function checkUserCollaboratorStatus(code) {
    const { access_token } = await getAccessToken(code)
    const { login } = await getUser(access_token)
    try {
        const isUserCollaborator = await AxiosInstance.get(`https://docs-access.dagger.io/u/${login}`)

        return {
            status: isUserCollaborator.status,
            access_token
        }
    } catch (error) {
        console.log("error checkUserCollaboratorStatus", error.message);
    }
}