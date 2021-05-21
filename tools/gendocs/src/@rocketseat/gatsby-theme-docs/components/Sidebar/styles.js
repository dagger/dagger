import styled from '@emotion/styled';

export const Container = styled.aside`
  width: 100%;
  overflow-y: auto;

  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-direction: column;

  position: sticky;
  top: 0;
  padding-top: 36px;
  transition: transform 0.5s;
  height: calc(100vh - 1px);

  nav {
    width: 100%;
    padding-top: 24px;
    align-self: flex-start;
    flex: 1;
  }

  @media (max-width: 780px) {
    max-width: 75%;
    min-width: auto;
    z-index: 1001;
    position: fixed;
    top: 0;
    bottom: 0;
    left: 0;
    padding-top: 32px;
    background: ${({ theme }) => theme.colors.background};
    transform: translate3d(
      ${({ isMenuOpen }) => (isMenuOpen ? '0' : '-100%')},
      0,
      0
    );
  }
`;

export const LogoContainer = styled.div`
  width: 70%;

  a {
    width: 100%;
    display: flex;
    justify-content: flex-start;
    align-items: center;
  }
`;

export const List = styled.ul`
  list-style: none;
  width: 100%;
  padding-left: 0;

  display: flex;
  justify-content: flex-start;
  align-items: center;
  flex-direction: column;
`;

export const Heading = styled.li`
  padding-left: 30px;
  width: 100%;
  text-transform: uppercase;
  font-size: 14px;
  font-weight: bold;
  margin-top: 20px;
  color: ${({ theme }) => theme.colors.title};
  letter-spacing: 0.142em;
`;

export const Item = styled.li`
  font-size: 15px;
  width: 100%;
  transition: all 200ms ease-in-out;
  padding: 0 20px;
  cursor: pointer;

  a,
  span {
    display: block;
    font-size: 15px;
    color: ${({ theme }) => theme.colors.text};
    background-color: ${({ theme }) => theme.colors.background};
    padding: 4px 10px;
    margin: 4px 0;
    border-radius: 4px;
    font-weight: normal;

    text-decoration: none;
    width: 100%;
    height: 100%;

    display: flex;
    justify-content: flex-start;
    align-items: center;

    cursor: pointer;
    margin: 0 auto;

    transition: all 0.2s ease;

    svg {
      width: 20px;
      height: 20px;
      margin-right: 10px;
    }

    &:not(.active-link):hover {
      color: ${({ theme }) => theme.colors.primary};
    }

    &.active-link {
      color: ${({ theme }) => theme.colors.primary};
      background-color: ${({ theme }) => theme.colors.shape};
    }

    @media (max-width: 780px) {
      &.active-link {
        background: ${({ theme }) => theme.colors.shape};
      }
    }
  }
`;

export const SubItem = styled(List)`
  margin: 5px 0 0 0;
`;

export const Select = styled.select`
  border: 1px solid #6C6C808C;
  padding: 0.5rem;
  margin: 2rem 0 1rem;
  width: 100%;
`;