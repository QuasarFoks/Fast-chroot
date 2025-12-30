#include <iostream>
#include <cstdlib>
#include <ostream>
using namespace std;
void show_help() {
    cout << "LXC Continer prototip" << endl;

}
void runtime() {
    cout << "Запуск в контейнере" << endl;
    system("sudo lxc-start -f config/chroot.conf -n chroot -F");
    cout << "Остановка контейнера" << endl;
    return;
}
int main(int args, char* argv[]) {
    if (args == 1) {
        show_help();
        return 0;
    }

    string action = argv[1];
    if ( action == "--use-runtime" ) {
        runtime();
    }
    else if ( action == "--help" ) {
        show_help();
        return 0;
    }

    return 0;
}
