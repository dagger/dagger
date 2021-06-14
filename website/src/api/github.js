import axios from 'axios';

const AxiosInstance = axios.create({
    headers: { 'Accept': 'application/vnd.github.v3+json' },
});

function bindApiCall({ url, config, errorMessage }) {
    try {
        const apiCall = AxiosInstance.get(url, {
            ...config,
            validateStatus: function (status) {
                return status < 500; // Resolve only if the status code is less than 500
            }
        })

        return apiCall
    } catch (error) {
        console.log(errorMessage, error.message)
    }
}

async function getAccessToken(code) {
    const accessToken = await bindApiCall({
        url: 'https://github.com/login/oauth/access_token',
        config: {
            params: {
                code,
                client_id: 'cd8f9be2562bfc8d6cfc',
                client_secret: '2509358055095d52dfd7331d072f378e7f16940f',
            },
            errorMessage: 'error getAccessToken'
        }
    })

    return accessToken.data
}

export async function getUser(access_token) {
    const user = await bindApiCall({
        url: 'https://api.github.com/user',
        config: {
            headers: { Authorization: `token ${access_token}` },
        },
        errorMessage: 'error getUser'
    })

    return {
        login: user.data?.login,
        error: user.data?.error_description,
        status: user.status
    }

}

export async function checkUserCollaboratorStatus(code) {
    const { access_token } = await getAccessToken(code)
    const { login } = await getUser(access_token)

    const isUserCollaborator = await bindApiCall({
        url: `https://docs-access.dagger.io/u/${login}`,
        errorMessage: 'error checkUserCollaboratorStatus'
    })

    return {
        isAllowed: isUserCollaborator.data,
        access_token
    }
}