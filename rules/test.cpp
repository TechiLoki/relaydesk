#include "core.hpp"
#include <functional>
#include <iostream>
#include <stdexcept>
void require(bool condition) {if(!condition)throw std::runtime_error("assertion failed");}
void rejected(const std::function<void()>& f) {
 bool failed=false;try{f();}catch(const std::invalid_argument&){failed=true;}require(failed);
}
int main() {
 auto r=relay::reconcile(6800,500,300,0,{{2,1800},{1,2400}});
 require(r.subtotal==6000 && r.total==6800 && r.units==3 && r.checked_lines);
 require(relay::reconcile(500,0,0,0,{}).checked_lines==false);
 require(relay::reconcile(0,0,0,100,{{1,100}}).total==0);
 rejected([]{relay::reconcile(20,0,0,0,{{0,20}});});
 rejected([]{relay::reconcile(20,0,0,0,{{-1,20}});});
 rejected([]{relay::reconcile(20,0,0,0,{{1,-20}});});
 rejected([]{relay::reconcile(20,0,0,0,{{1,30}});});
 rejected([]{relay::reconcile(0,0,0,101,{{1,100}});});
 rejected([]{relay::reconcile(1,0,0,0,{{10000,relay::limit}});});
 rejected([]{relay::reconcile(1,relay::limit,1,0,{{1,1}});});
 rejected([]{relay::reconcile(1,0,0,0,std::vector<relay::Line>(501,{1,1}));});
 require(relay::reconcile(relay::limit,0,0,0,{{1,relay::limit}}).total==relay::limit);
 std::cout<<"12 C++ reconciliation checks passed\n";
}
